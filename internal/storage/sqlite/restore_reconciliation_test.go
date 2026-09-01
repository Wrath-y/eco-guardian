package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

func TestReconcileRestoredJobsTerminalizesSnapshotWorkButKeepsCoordinator(t *testing.T) {
	ctx := context.Background()
	opened := newStore(t)
	hash := strings.Repeat("a", 64)
	request := sharedjob.Request{ProjectID: opened.ProjectID(), Kind: "backup", InputHash: hash, IdempotencyKey: "snapshot-backup", RequestHash: hash}
	backupJob, _, err := opened.CreateOrGet(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	backupJob, _, err = opened.Transition(ctx, backupJob.ID, sharedjob.Queued, sharedjob.Running, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = opened.Append(ctx, sharedjob.Event{JobID: backupJob.ID, Ordinal: 1, Phase: "online_backup", Progress: 65, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	restoreID, _ := domain.NewID()
	now := time.Now().UTC()
	restoreJob := sharedjob.Record{ID: restoreID, ProjectID: opened.ProjectID(), Kind: "restore", InputHash: hash, IdempotencyKey: "restore-coordinator", RequestHash: hash, Status: sharedjob.Running, CreatedAt: now, UpdatedAt: now}
	if err = opened.ReconcileRestoreJob(ctx, restoreJob, 1, 2); err != nil {
		t.Fatal(err)
	}
	if err = opened.ReconcileRestoredJobs(ctx, restoreID); err != nil {
		t.Fatal(err)
	}
	reconciled, err := opened.GetJob(ctx, backupJob.ID)
	if err != nil || reconciled.Status != sharedjob.Failed {
		t.Fatalf("snapshot job=%#v err=%v", reconciled, err)
	}
	events, err := opened.ListEvents(ctx, backupJob.ID, 0)
	if err != nil || len(events) != 2 || events[1].Ordinal != 2 || events[1].Progress != 65 || events[1].SafeError != "JOB_SUPERSEDED_BY_RESTORE" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	coordinator, err := opened.GetJob(ctx, restoreID)
	if err != nil || coordinator.Status != sharedjob.Interrupted {
		t.Fatalf("coordinator=%#v err=%v", coordinator, err)
	}
	if err = opened.ReconcileRestoredJobs(ctx, restoreID); err != nil {
		t.Fatalf("idempotent reconciliation: %v", err)
	}
}
