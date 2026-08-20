package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

func TestGraphSyncStateUsesGenerationCAS(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("graphstate"))
	if err != nil {
		t.Fatal(err)
	}
	initial := graphsync.SyncState{RevisionID: string(revision.ID), Pipeline: graphsync.StateQueued, Warnings: []string{}}
	if err = store.CreateGraphSyncState(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := store.GetGraphSyncState(context.Background(), revision.ID)
	if err != nil || !found || loaded.Generation != 0 {
		t.Fatalf("%#v %v", loaded, err)
	}
	next := loaded
	next.Pipeline, next.Generation, next.Warnings = graphsync.StateBuilding, 1, []string{"VECTOR_DEGRADED"}
	next.ProviderRequestID, next.ProviderTaskID = "request-1", "task-1"
	updated, swapped, err := store.CompareAndSwapGraphSyncState(context.Background(), loaded, next)
	if err != nil || !swapped || updated.Generation != 1 {
		t.Fatalf("%#v %v %v", updated, swapped, err)
	}
	if _, swapped, err = store.CompareAndSwapGraphSyncState(context.Background(), loaded, next); err != nil || swapped {
		t.Fatalf("stale CAS: %v %v", swapped, err)
	}
	illegal := loaded
	illegal.Pipeline, illegal.Generation = graphsync.StateReady, 1
	if _, _, err = store.CompareAndSwapGraphSyncState(context.Background(), loaded, illegal); err == nil {
		t.Fatal("illegal pipeline jump accepted")
	}
	loaded, found, err = store.GetGraphSyncState(context.Background(), revision.ID)
	if err != nil || !found || len(loaded.Warnings) != 1 || loaded.Warnings[0] != "VECTOR_DEGRADED" || loaded.ProviderRequestID != "request-1" || loaded.ProviderTaskID != "task-1" {
		t.Fatalf("%#v %v", loaded, err)
	}
}

func TestProviderTaskIdentityIsUniqueAcrossRecoverableStates(t *testing.T) {
	store := newStore(t)
	_, firstRevision, err := store.Create(context.Background(), "tag", tagDraft("firsttask"))
	if err != nil {
		t.Fatal(err)
	}
	_, secondRevision, err := store.Create(context.Background(), "tag", tagDraft("secondtask"))
	if err != nil {
		t.Fatal(err)
	}
	first := graphsync.SyncState{RevisionID: string(firstRevision.ID), Pipeline: graphsync.StateQueued, Warnings: []string{}}
	second := graphsync.SyncState{RevisionID: string(secondRevision.ID), Pipeline: graphsync.StateQueued, Warnings: []string{}}
	if err = store.CreateGraphSyncState(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err = store.CreateGraphSyncState(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	first.Pipeline, first.Generation, first.ProviderTaskID = graphsync.StateBuilding, 1, "provider-task"
	if _, swapped, swapErr := store.CompareAndSwapGraphSyncState(context.Background(), graphsync.SyncState{RevisionID: string(firstRevision.ID), Pipeline: graphsync.StateQueued, Warnings: []string{}}, first); swapErr != nil || !swapped {
		t.Fatalf("first swapped=%v err=%v", swapped, swapErr)
	}
	second.Pipeline, second.Generation, second.ProviderTaskID = graphsync.StateBuilding, 1, "provider-task"
	if _, _, err = store.CompareAndSwapGraphSyncState(context.Background(), graphsync.SyncState{RevisionID: string(secondRevision.ID), Pipeline: graphsync.StateQueued, Warnings: []string{}}, second); err == nil {
		t.Fatal("duplicate provider task identity accepted")
	}
	jobRequest := graphsync.GraphJobRequest{ProjectID: store.ProjectID(), RevisionID: firstRevision.ID, InputHash: firstRevision.ConfigHash, IdempotencyKey: "retry-foreign-key", RequestHash: strings.Repeat("a", 64)}
	job, _, err := store.CreateOrGetGraphJob(context.Background(), jobRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`UPDATE jobs SET retry_of_job_id=? WHERE id=?`, "missing-job", job.ID); err == nil {
		t.Fatal("retry link accepted a missing job")
	}
}

func TestMarkGraphReadyCommitsOneHandoff(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("ready"))
	if err != nil {
		t.Fatal(err)
	}
	state := graphsync.SyncState{RevisionID: string(revision.ID), Pipeline: graphsync.StateBuilding, Warnings: []string{}}
	if err = store.CreateGraphSyncState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	ready, swapped, err := store.MarkGraphReady(context.Background(), state, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil || !swapped || ready.Pipeline != graphsync.StateReady || ready.Generation != 1 {
		t.Fatalf("%#v %v %v", ready, swapped, err)
	}
	if _, swapped, err = store.MarkGraphReady(context.Background(), state, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil || swapped {
		t.Fatalf("stale=%v err=%v", swapped, err)
	}
	var count int
	if err = store.db.QueryRow(`SELECT count(*) FROM graph_impact_handoffs WHERE revision_id=?`, revision.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestMarkGraphReadyCompletesLinkedRunningJob(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("readyjob"))
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := store.CreateOrGetGraphJob(context.Background(), graphsync.GraphJobRequest{ProjectID: store.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "ready-job", RequestHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, swapped, err := store.TransitionGraphJob(context.Background(), job.ID, graphsync.JobQueued, graphsync.JobRunning, nil); err != nil || !swapped {
		t.Fatalf("job start swapped=%v err=%v", swapped, err)
	}
	state := graphsync.SyncState{RevisionID: string(revision.ID), Pipeline: graphsync.StateBuilding, LatestJobID: string(job.ID), Warnings: []string{}}
	if err = store.CreateGraphSyncState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if _, swapped, err := store.MarkGraphReady(context.Background(), state, strings.Repeat("a", 64)); err != nil || !swapped {
		t.Fatalf("ready swapped=%v err=%v", swapped, err)
	}
	stored, err := store.GetGraphJob(context.Background(), job.ID)
	if err != nil || stored.Status != graphsync.JobSucceeded || stored.Result == nil || stored.Result.ID != revision.ID {
		t.Fatalf("job=%#v err=%v", stored, err)
	}
}

func TestCommitGraphReadyAtomicallyStoresProjectionEvidence(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("summaryready"))
	if err != nil {
		t.Fatal(err)
	}
	state := graphsync.SyncState{RevisionID: string(revision.ID), Pipeline: graphsync.StateBuilding, Warnings: []string{}}
	if err = store.CreateGraphSyncState(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	summary := projector.Summary{ProjectID: string(store.ProjectID()), RevisionID: string(revision.ID), ConfigHash: revision.ConfigHash, SchemaVersion: projector.ProjectionSchemaV1, ProjectorVersion: projector.ProjectorV1, ManifestHash: strings.Repeat("a", 64)}
	if _, swapped, err := store.CommitGraphReady(context.Background(), state, summary, `{"verified":true}`); err != nil || !swapped {
		t.Fatalf("ready swapped=%v err=%v", swapped, err)
	}
	if stored, found, err := store.GetProjectionSummary(context.Background(), revision.ID, projector.ProjectionSchemaV1, projector.ProjectorV1); err != nil || !found || stored.ManifestHash != summary.ManifestHash {
		t.Fatalf("summary=%#v found=%v err=%v", stored, found, err)
	}
}

func TestListRecoverableGraphSyncStatesFiltersTerminalStates(t *testing.T) {
	store := newStore(t)
	_, queuedRevision, err := store.Create(context.Background(), "tag", tagDraft("queued"))
	if err != nil {
		t.Fatal(err)
	}
	_, readyRevision, err := store.Create(context.Background(), "tag", tagDraft("terminal"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateGraphSyncState(context.Background(), graphsync.SyncState{RevisionID: string(queuedRevision.ID), Pipeline: graphsync.StateQueued, ProviderRequestID: "request-queued", ProviderTaskID: "task-queued", Warnings: []string{}}); err != nil {
		t.Fatal(err)
	}
	if err = store.CreateGraphSyncState(context.Background(), graphsync.SyncState{RevisionID: string(readyRevision.ID), Pipeline: graphsync.StateReady, Warnings: []string{}}); err != nil {
		t.Fatal(err)
	}
	states, err := store.ListRecoverableGraphSyncStates(context.Background(), 10)
	if err != nil || len(states) != 1 || states[0].RevisionID != string(queuedRevision.ID) || states[0].ProviderRequestID != "request-queued" || states[0].ProviderTaskID != "task-queued" {
		t.Fatalf("%#v %v", states, err)
	}
}
