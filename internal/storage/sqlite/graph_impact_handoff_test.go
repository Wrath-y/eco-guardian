package sqlite

import (
	"context"
	"strings"
	"testing"
)

func TestGraphImpactHandoffIsUniqueAndReplaySafe(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("impact"))
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateGraphImpactHandoff(context.Background(), revision.ID, strings.Repeat("a", 64), "impact")
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	created, err = store.CreateGraphImpactHandoff(context.Background(), revision.ID, strings.Repeat("a", 64), "impact")
	if err != nil || created {
		t.Fatalf("replay created=%v err=%v", created, err)
	}
	if status, found, statusErr := store.GraphImpactHandoffStatus(context.Background(), revision.ID, strings.Repeat("a", 64)); statusErr != nil || !found || status != "queued" {
		t.Fatalf("status=%q found=%v err=%v", status, found, statusErr)
	}
}
