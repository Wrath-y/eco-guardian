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
	updated, swapped, err := store.CompareAndSwapGraphSyncState(context.Background(), loaded, next)
	if err != nil || !swapped || updated.Generation != 1 {
		t.Fatalf("%#v %v %v", updated, swapped, err)
	}
	if _, swapped, err = store.CompareAndSwapGraphSyncState(context.Background(), loaded, next); err != nil || swapped {
		t.Fatalf("stale CAS: %v %v", swapped, err)
	}
	loaded, found, err = store.GetGraphSyncState(context.Background(), revision.ID)
	if err != nil || !found || len(loaded.Warnings) != 1 || loaded.Warnings[0] != "VECTOR_DEGRADED" {
		t.Fatalf("%#v %v", loaded, err)
	}
}
