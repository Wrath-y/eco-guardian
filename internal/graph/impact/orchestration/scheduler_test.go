package orchestration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	"github.com/zouyi/eco-guardian/internal/graph/impact/planner"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type baselineFake struct {
	id    domain.ID
	found bool
}

func (f *baselineFake) ActiveBaseline(context.Context) (domain.ID, bool, error) {
	return f.id, f.found, nil
}

type handoffStateFake struct {
	status, reason string
	base, job      domain.ID
}

func (f *handoffStateFake) SetImpactHandoffState(_ context.Context, _ domain.ID, _, status string, base, job, _ domain.ID, reason string) error {
	f.status, f.reason, f.base, f.job = status, reason, base, job
	return nil
}

type admissionFake struct {
	project domain.ID
	records map[domain.ID]versioningrevision.Record
	graphs  map[domain.ID]string
}

func (f admissionFake) ReadProjectionRevision(_ context.Context, id domain.ID) (projector.Revision, error) {
	record := f.records[id]
	return projector.Revision{ProjectID: f.project, RevisionID: id, ConfigHash: record.Metadata.ConfigHash}, nil
}
func (f admissionFake) GetRevisionRecord(_ context.Context, id domain.ID) (versioningrevision.Record, error) {
	return f.records[id], nil
}
func (f admissionFake) Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error) {
	return validation.GatePass, nil
}
func (f admissionFake) GraphProjectionSummary(_ context.Context, id domain.ID) (projector.Summary, bool, error) {
	record := f.records[id]
	return projector.Summary{ProjectID: string(f.project), RevisionID: string(id), ConfigHash: record.Metadata.ConfigHash, SchemaVersion: projector.ProjectionSchemaV1, ProjectorVersion: projector.ProjectorV1, ManifestHash: f.graphs[id]}, true, nil
}
func (f admissionFake) InspectSnapshot(_ context.Context, namespace, version, _ string) (graphsync.Snapshot, error) {
	id := domain.ID(version)
	return graphsync.Snapshot{Namespace: namespace, Version: version, ContentHash: f.graphs[id], Status: "ready", QueryReady: true, Components: []graphsync.Component{{Name: "graph", State: "ready"}, {Name: "fts", State: "ready"}}}, nil
}

type schedulerJobs struct{ record sharedjob.Record }

func (s *schedulerJobs) CreateOrGet(_ context.Context, request sharedjob.Request) (sharedjob.Record, bool, error) {
	if s.record.ID.Valid() {
		return s.record, true, nil
	}
	id, _ := domain.NewID()
	s.record = sharedjob.Record{ID: id, ProjectID: request.ProjectID, Kind: request.Kind, RevisionID: request.RevisionID, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, Status: sharedjob.Queued, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	return s.record, false, nil
}
func (s *schedulerJobs) GetJob(context.Context, domain.ID) (sharedjob.Record, error) {
	return s.record, nil
}
func (s *schedulerJobs) Transition(context.Context, domain.ID, sharedjob.Status, sharedjob.Status, *sharedjob.Result, int64) (sharedjob.Record, bool, error) {
	return s.record, true, nil
}
func (s *schedulerJobs) RequestCancellation(context.Context, domain.ID) (sharedjob.Record, bool, error) {
	return s.record, false, nil
}

type schedulerReports struct{ staging domain.ID }

func (s *schedulerReports) CreateOrGetStaging(context.Context, domain.ID, impact.Input, string) (domain.ID, bool, error) {
	if !s.staging.Valid() {
		s.staging, _ = domain.NewID()
	}
	return s.staging, false, nil
}
func (*schedulerReports) LoadStaging(context.Context, domain.ID) (domain.ID, impact.Input, string, string, bool, error) {
	return "", impact.Input{}, "", "", false, nil
}
func (*schedulerReports) SaveChanged(context.Context, domain.ID, []impact.ChangedEntity) error {
	return nil
}
func (*schedulerReports) SaveTraversal(context.Context, domain.ID, []impact.AffectedEntity, []impact.TruncationReason, []string) error {
	return nil
}
func (*schedulerReports) SaveDefaultPaths(context.Context, domain.ID, []impact.AffectedEntity) error {
	return nil
}
func (*schedulerReports) SaveSuspected(context.Context, domain.ID, impact.SuspectedState, []impact.SuspectedEvidence, []string) error {
	return nil
}
func (*schedulerReports) Seal(_ context.Context, value impact.Report) (impact.Report, bool, error) {
	return value, false, nil
}
func (*schedulerReports) GetImpactReport(context.Context, domain.ID) (impact.Report, error) {
	return impact.Report{}, nil
}
func (*schedulerReports) FindByInputHash(context.Context, string) (impact.Report, bool, error) {
	return impact.Report{}, false, nil
}
func (*schedulerReports) ExpandPaths(context.Context, domain.ID, string, string, []impact.Path) (bool, error) {
	return false, nil
}
func (*schedulerReports) SaveExplanation(context.Context, domain.ID, impact.ExplanationAttempt) error {
	return nil
}
func (*schedulerReports) FindExplanation(context.Context, domain.ID, string) (impact.ExplanationAttempt, bool, error) {
	return impact.ExplanationAttempt{}, false, nil
}

func revisionRecord(t *testing.T, id domain.ID, configHash string) versioningrevision.Record {
	t.Helper()
	manifest := versioningrevision.VersionManifest{Entries: []versioningrevision.VersionEntry{
		{CapabilityID: "schema", ContractVersion: "validation-v1", ImplementationVersion: "v1", State: versioningrevision.Registered},
		{CapabilityID: "dsl", ContractVersion: "validation-v1", ImplementationVersion: "v1", State: versioningrevision.Registered},
		{CapabilityID: "validator-registry", ContractVersion: "validation-v1", ImplementationVersion: "v1", State: versioningrevision.Registered},
		{CapabilityID: "numeric-policy", ContractVersion: "validation-v1", ImplementationVersion: "v1", State: versioningrevision.Registered},
	}}
	manifestHash, err := manifest.Hash()
	if err != nil {
		t.Fatal(err)
	}
	return versioningrevision.Record{DisplayRevision: 1, Metadata: versioningrevision.Metadata{RevisionID: id, ConfigHash: configHash, Manifest: manifest, ManifestHash: manifestHash, CreatedAt: time.Now().UTC()}}
}

func TestAutomaticHandoffWaitsWithoutBaselineThenClaimsOnWakeup(t *testing.T) {
	projectID, _ := domain.NewID()
	baseID, _ := domain.NewID()
	targetID, _ := domain.NewID()
	configA, configB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	graphA, graphB := strings.Repeat("c", 64), strings.Repeat("d", 64)
	fake := admissionFake{project: projectID, records: map[domain.ID]versioningrevision.Record{baseID: revisionRecord(t, baseID, configA), targetID: revisionRecord(t, targetID, configB)}, graphs: map[domain.ID]string{baseID: graphA, targetID: graphB}}
	baseline := &baselineFake{id: baseID, found: false}
	handoffs := &handoffStateFake{}
	jobs, reports := &schedulerJobs{}, &schedulerReports{}
	scheduler := Scheduler{ProjectID: projectID, Baselines: baseline, Admission: planner.Admission{Revisions: fake, Validation: fake, Summaries: fake, Graph: fake}, Submitter: Submitter{Jobs: jobs, Reports: reports, Clock: testClock{now: time.Now().UTC()}}, Handoffs: handoffs}
	handoff := graphsync.ImpactHandoff{RevisionID: targetID, GraphHash: graphB, Stage: "impact"}
	if err := scheduler.EnqueueImpact(context.Background(), handoff); err != nil || handoffs.status != "waiting" || handoffs.reason != "NO_BASELINE" || handoffs.job != "" {
		t.Fatalf("waiting=%#v err=%v", handoffs, err)
	}
	baseline.found = true
	if err := scheduler.EnqueueImpact(context.Background(), handoff); err != nil || handoffs.status != "claimed" || handoffs.reason != "" || handoffs.base != baseID || !handoffs.job.Valid() {
		t.Fatalf("claimed=%#v err=%v", handoffs, err)
	}
}

var _ versioningdiff.ActiveBaselineReader = (*baselineFake)(nil)
