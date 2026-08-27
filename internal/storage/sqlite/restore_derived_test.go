package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

func TestRestoreGraphInvalidationRecordsGenerationAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	opened := newStore(t)
	descriptor := projector.Descriptor{SchemaVersion: projector.ProjectionSchemaV1, Version: projector.ProjectorV1, Relations: projector.V1Relations(), Formatter: projector.V1Formatter{}}
	if err := opened.RegisterGraphVersionContributor(projector.VersionContributor{Descriptor: descriptor}); err != nil {
		t.Fatal(err)
	}
	_, revision, err := opened.Create(ctx, "tag", tagDraft("restore_graph"))
	if err != nil {
		t.Fatal(err)
	}
	if err = opened.CreateGraphSyncState(ctx, graphsync.SyncState{RevisionID: string(revision.ID), Pipeline: graphsync.StateReady, Warnings: []string{}}); err != nil {
		t.Fatal(err)
	}
	record, err := opened.GetRevisionRecord(ctx, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	versions, available := restoreValidationVersions(record.Metadata.Manifest)
	if !available {
		t.Fatal("registered projector identities were not retained")
	}
	oldJob, _, err := opened.CreateOrGetGraphJob(ctx, graphsync.AutomaticGraphJobRequest(opened.ProjectID(), revision.ID, revision.ConfigHash, versions, nil))
	if err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	job, _, err := opened.CreateOrGet(ctx, sharedjob.Request{ProjectID: opened.ProjectID(), Kind: "restore", InputHash: hash, IdempotencyKey: "restore-derived", RequestHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if err = opened.ReconcileRestoreJob(ctx, job, 4, 7); err != nil {
		t.Fatal(err)
	}
	if err = opened.InvalidateAfterRestore(ctx, opened.ProjectID(), job.ID, hash); err != nil {
		t.Fatal(err)
	}
	if err = opened.InvalidateAfterRestore(ctx, opened.ProjectID(), job.ID, hash); err != nil {
		t.Fatal(err)
	}
	if err = opened.EnqueueFullRebuild(ctx, opened.ProjectID(), job.ID, hash); err != nil {
		t.Fatal(err)
	}
	if err = opened.EnqueueFullRebuild(ctx, opened.ProjectID(), job.ID, hash); err != nil {
		t.Fatal(err)
	}
	state, found, err := opened.GetGraphSyncState(ctx, revision.ID)
	if err != nil || !found || state.Pipeline != graphsync.StateQueued || state.Generation != 3 || len(state.Warnings) != 1 || state.Warnings[0] != "RESTORE_PENDING_VERIFICATION" {
		t.Fatalf("state=%#v found=%v err=%v", state, found, err)
	}
	graphJob, err := opened.GetGraphJob(ctx, domain.ID(state.LatestJobID))
	if err != nil || graphJob.ID == oldJob.ID || graphJob.Status != graphsync.JobQueued || !strings.Contains(graphJob.IdempotencyKey, string(job.ID)) || !strings.Contains(graphJob.Evidence, `"intent":"restore-full-rebuild"`) {
		t.Fatalf("graph job=%#v err=%v", graphJob, err)
	}
	var graphJobs int
	if err = opened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='graph_sync' AND revision_id=?`, revision.ID).Scan(&graphJobs); err != nil || graphJobs != 2 {
		t.Fatalf("graph jobs=%d err=%v", graphJobs, err)
	}
	var generation int64
	if err = opened.db.QueryRowContext(ctx, `SELECT restore_generation FROM restore_graph_invalidations WHERE job_id=?`, job.ID).Scan(&generation); err != nil || generation != 7 {
		t.Fatalf("generation=%d err=%v", generation, err)
	}
}

func TestRestoreGraphRebuildWithoutStoredProjectorIdentityRemainsPending(t *testing.T) {
	ctx := context.Background()
	opened := newStore(t)
	_, revision, err := opened.Create(ctx, "tag", tagDraft("restore_graph_missing_projector"))
	if err != nil {
		t.Fatal(err)
	}
	if err = opened.CreateGraphSyncState(ctx, graphsync.SyncState{RevisionID: string(revision.ID), Pipeline: graphsync.StateReady, Warnings: []string{}}); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("b", 64)
	job, _, err := opened.CreateOrGet(ctx, sharedjob.Request{ProjectID: opened.ProjectID(), Kind: "restore", InputHash: hash, IdempotencyKey: "restore-missing-projector", RequestHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if err = opened.ReconcileRestoreJob(ctx, job, 2, 3); err != nil {
		t.Fatal(err)
	}
	if err = opened.InvalidateAfterRestore(ctx, opened.ProjectID(), job.ID, hash); err != nil {
		t.Fatal(err)
	}
	if err = opened.EnqueueFullRebuild(ctx, opened.ProjectID(), job.ID, hash); err != nil {
		t.Fatal(err)
	}
	state, found, err := opened.GetGraphSyncState(ctx, revision.ID)
	if err != nil || !found || state.Pipeline != graphsync.StateSaved || state.Generation != 1 || state.LatestJobID != "" {
		t.Fatalf("pending state=%#v found=%v err=%v", state, found, err)
	}
	var graphJobs int
	if err = opened.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE kind='graph_sync' AND revision_id=?`, revision.ID).Scan(&graphJobs); err != nil || graphJobs != 0 {
		t.Fatalf("graph jobs=%d err=%v", graphJobs, err)
	}
}
