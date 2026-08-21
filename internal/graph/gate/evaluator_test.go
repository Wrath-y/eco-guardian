package gate

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type providerFake struct {
	snapshot graphsync.Snapshot
	err      error
}

func (f providerFake) Health(context.Context, string) (graphsync.Health, error) {
	return graphsync.Health{}, nil
}
func (f providerFake) InspectSnapshot(context.Context, string, string, string) (graphsync.Snapshot, error) {
	return f.snapshot, f.err
}
func (providerFake) PutSnapshot(context.Context, string, string, graphsync.PutSnapshotRequest, string) (graphsync.Snapshot, error) {
	return graphsync.Snapshot{}, nil
}
func (providerFake) GetTask(context.Context, string, string) (graphsync.Task, error) {
	return graphsync.Task{}, nil
}
func (providerFake) ActivateSnapshot(context.Context, string, string, string) (graphsync.Activation, error) {
	return graphsync.Activation{}, nil
}
func (providerFake) DeleteSnapshotForRetry(context.Context, string, string, string) error { return nil }

func TestEvaluatorRequiresExactReadySnapshotAndKeepsVectorWarning(t *testing.T) {
	projectID, _ := domain.NewID()
	revisionID, _ := domain.NewID()
	policyID, _ := domain.NewID()
	jobID, _ := domain.NewID()
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	summary := projector.Summary{ProjectID: string(projectID), RevisionID: string(revisionID), ConfigHash: hash, SchemaVersion: projector.ProjectionSchemaV1, ProjectorVersion: projector.ProjectorV1, ManifestHash: hash, NodeCount: 1}
	request := EvaluationRequest{Candidate: versioningrevision.CandidateContext{RevisionID: revisionID, ConfigHash: hash, ManifestHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", PolicyID: policyID}, Summary: summary, State: graphsync.SyncState{RevisionID: string(revisionID), Pipeline: graphsync.StateReady, LatestJobID: string(jobID), Warnings: []string{}}, Job: &graphsync.GraphJob{ID: jobID, ProjectID: projectID, RevisionID: revisionID, InputHash: hash, IdempotencyKey: "gate-job", RequestHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Status: graphsync.JobSucceeded, Result: &graphsync.GraphJobResult{Type: "graph_sync", ID: revisionID, URL: "/graph"}}, RequestID: "gate-request"}
	provider := providerFake{snapshot: graphsync.Snapshot{Namespace: string(projectID), Version: string(revisionID), ContentHash: hash, NodeCount: 1, Status: "ready", QueryReady: true, Components: []graphsync.Component{{Name: "graph", State: "ready"}, {Name: "fts", State: "ready"}, {Name: "vector", State: "unavailable"}}}}
	result := (Evaluator{Provider: provider}).Evaluate(context.Background(), request)
	if result.State != versioninggate.Pass || len(result.Evidence) != 2 || len(result.Warnings) != 1 || result.Warnings[0] != "DEGRADED_VECTOR" {
		t.Fatalf("result=%#v", result)
	}
	provider.snapshot.ContentHash = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if stale := (Evaluator{Provider: provider}).Evaluate(context.Background(), request); stale.State != versioninggate.Stale {
		t.Fatalf("stale=%#v", stale)
	}
	provider.snapshot.ContentHash, provider.snapshot.Components[1].State = hash, "building"
	if blocked := (Evaluator{Provider: provider}).Evaluate(context.Background(), request); blocked.State != versioninggate.Block {
		t.Fatalf("blocked=%#v", blocked)
	}
}
