package sqlite

import (
	"context"
	"testing"

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
