package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type graphAppRevisionFake struct {
	project domain.ID
	record  versioningrevision.Record
}

func (f graphAppRevisionFake) ProjectID() domain.ID { return f.project }
func (f graphAppRevisionFake) GetRevisionRecord(_ context.Context, id domain.ID) (versioningrevision.Record, error) {
	if id != f.record.Metadata.RevisionID {
		return versioningrevision.Record{}, ErrGraphRevisionUnavailable
	}
	return f.record, nil
}

type graphAppJobsFake struct{ job graphsync.GraphJob }

func (f *graphAppJobsFake) CreateOrGetGraphJob(_ context.Context, request graphsync.GraphJobRequest) (graphsync.GraphJob, bool, error) {
	f.job.ProjectID, f.job.RevisionID, f.job.InputHash, f.job.IdempotencyKey, f.job.RequestHash = request.ProjectID, request.RevisionID, request.InputHash, request.IdempotencyKey, request.RequestHash
	return f.job, false, nil
}
func (f *graphAppJobsFake) GetGraphJob(_ context.Context, id domain.ID) (graphsync.GraphJob, error) {
	if id != f.job.ID {
		return graphsync.GraphJob{}, ErrGraphRetryInvalid
	}
	return f.job, nil
}
func (f *graphAppJobsFake) TransitionGraphJob(_ context.Context, id domain.ID, expected, next graphsync.JobStatus, _ *graphsync.GraphJobResult) (graphsync.GraphJob, bool, error) {
	if id != f.job.ID || f.job.Status != expected || !expected.CanTransitionTo(next) {
		return graphsync.GraphJob{}, false, ErrGraphRetryInvalid
	}
	f.job.Status = next
	return f.job, true, nil
}

type graphAppStatesFake struct{ state graphsync.SyncState }

func (f graphAppStatesFake) GetGraphSyncState(_ context.Context, id domain.ID) (graphsync.SyncState, bool, error) {
	return f.state, id == domain.ID(f.state.RevisionID), nil
}

type graphAppSummariesFake struct{ summary projector.Summary }

func (f graphAppSummariesFake) GraphProjectionSummary(_ context.Context, id domain.ID) (projector.Summary, bool, error) {
	return f.summary, id == domain.ID(f.summary.RevisionID), nil
}

type graphAppValidationFake struct{ result validation.GateResult }

func (f graphAppValidationFake) Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error) {
	return f.result, nil
}

type graphAppProviderFake struct {
	snapshot  graphsync.Snapshot
	namespace string
	version   string
	puts      int
}

func (f *graphAppProviderFake) Health(context.Context, string) (graphsync.Health, error) {
	return graphsync.Health{}, nil
}
func (f *graphAppProviderFake) InspectSnapshot(_ context.Context, namespace, version, _ string) (graphsync.Snapshot, error) {
	f.namespace, f.version = namespace, version
	return f.snapshot, nil
}
func (f *graphAppProviderFake) PutSnapshot(context.Context, string, string, graphsync.PutSnapshotRequest, string) (graphsync.Snapshot, error) {
	f.puts++
	return graphsync.Snapshot{}, nil
}
func (*graphAppProviderFake) GetTask(context.Context, string, string) (graphsync.Task, error) {
	return graphsync.Task{}, nil
}
func (*graphAppProviderFake) ActivateSnapshot(context.Context, string, string, string) (graphsync.Activation, error) {
	return graphsync.Activation{}, nil
}
func (*graphAppProviderFake) DeleteSnapshotForRetry(context.Context, string, string, string) error {
	return nil
}

func TestGraphStatusUsesExactRevisionAndReadOnlyProviderInspection(t *testing.T) {
	projectID := domain.ID("01948c1e-0000-7000-8000-000000000000")
	revisionID := domain.ID("01948c1e-0000-7000-8000-000000000001")
	jobID := domain.ID("01948c1e-0000-7000-8000-000000000002")
	hash := strings.Repeat("a", 64)
	manifest := versioningrevision.VersionManifest{Entries: []versioningrevision.VersionEntry{{CapabilityID: "schema", ContractVersion: "1", ImplementationVersion: "schema-v1", State: versioningrevision.Registered}, {CapabilityID: "dsl", ContractVersion: "1", ImplementationVersion: "dsl-v1", State: versioningrevision.Registered}, {CapabilityID: "validator-registry", ContractVersion: "1", ImplementationVersion: "registry-v1", State: versioningrevision.Registered}, {CapabilityID: "numeric-policy", ContractVersion: "1", ImplementationVersion: "numeric-v1", State: versioningrevision.Registered}}}
	record := versioningrevision.Record{DisplayRevision: 1, Metadata: versioningrevision.Metadata{RevisionID: revisionID, ConfigHash: hash, Manifest: manifest, ManifestHash: strings.Repeat("b", 64), CreatedAt: time.Now().UTC()}}
	job := graphsync.GraphJob{ID: jobID, ProjectID: projectID, RevisionID: revisionID, InputHash: hash, IdempotencyKey: "automatic", RequestHash: strings.Repeat("c", 64), Status: graphsync.JobSucceeded}
	summary := projector.Summary{ProjectID: string(projectID), RevisionID: string(revisionID), ConfigHash: hash, SchemaVersion: projector.ProjectionSchemaV1, ProjectorVersion: projector.ProjectorV1, ManifestHash: strings.Repeat("d", 64), NodeCount: 3, EdgeCount: 2}
	provider := &graphAppProviderFake{snapshot: graphsync.Snapshot{Namespace: string(projectID), Version: string(revisionID), Status: "ready", QueryReady: true, Components: []graphsync.Component{{Name: "graph", State: "ready"}, {Name: "fts", State: "ready"}}}}
	service := GraphSyncApplication{
		Revisions:  graphAppRevisionFake{project: projectID, record: record},
		Jobs:       &graphAppJobsFake{job: job},
		States:     graphAppStatesFake{state: graphsync.SyncState{RevisionID: string(revisionID), Pipeline: graphsync.StateReady, LatestJobID: string(jobID), Warnings: []string{"DEGRADED_VECTOR"}}},
		Summaries:  graphAppSummariesFake{summary: summary},
		Validation: graphAppValidationFake{result: validation.GatePass},
		Provider:   provider,
		Clock:      func() time.Time { return time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC) },
	}
	status, err := service.GraphStatus(context.Background(), revisionID)
	if err != nil || !status.Freshness.Fresh || status.Summary == nil || status.Summary.ManifestHash != summary.ManifestHash || status.Provider == nil || provider.namespace != string(projectID) || provider.version != string(revisionID) || provider.puts != 0 || len(status.Warnings) != 1 {
		t.Fatalf("status=%#v err=%v provider=%#v", status, err, provider)
	}
}
