package sqlite

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestCreatePatchListAndReopen(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, projectID, err := Create(context.Background(), dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	draft := domain.EntityDraft{Key: "fire", Name: "Fire", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}}
	entity, first, err := store.Create(context.Background(), domain.KindTag, draft)
	if err != nil {
		t.Fatal(err)
	}
	if first.DisplayRevision != 1 || !entity.ID.Valid() {
		t.Fatalf("create = %#v %#v", entity, first)
	}
	page, err := store.List(context.Background(), domain.KindTag, "fire", "", 50)
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list = %#v %v", page, err)
	}
	next, second, err := store.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"Flame"`)})
	if err != nil {
		t.Fatal(err)
	}
	if next.EntityVersion != 2 || second.DisplayRevision != 2 {
		t.Fatalf("patch = %#v %#v", next, second)
	}
	if _, _, err := store.Patch(context.Background(), domain.KindTag, entity.ID, 1, domain.EntityPatch{"name": json.RawMessage(`"stale"`)}); err != ErrRevisionConflict {
		t.Fatalf("stale patch = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, gotID, err := Open(dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if gotID != projectID {
		t.Fatalf("project identity changed: %s %s", projectID, gotID)
	}
	got, err := reopened.Get(context.Background(), domain.KindTag, entity.ID)
	if err != nil || got.Name != "Flame" {
		t.Fatalf("reopened get = %#v %v", got, err)
	}
}
