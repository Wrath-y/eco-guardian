package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	r, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := Create(context.Background(), t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func tagDraft(key string) domain.EntityDraft {
	return domain.EntityDraft{Key: key, Name: key, Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}}
}

func TestConnectionPragmasAndConstraints(t *testing.T) {
	s := newStore(t)
	for _, assertion := range []struct{ query, want string }{{"PRAGMA foreign_keys", "1"}, {"PRAGMA journal_mode", "wal"}, {"PRAGMA busy_timeout", "5000"}} {
		var got string
		if err := s.db.QueryRow(assertion.query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != assertion.want {
			t.Fatalf("%s = %q, want %q", assertion.query, got, assertion.want)
		}
	}
	if _, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire")); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate = %v", err)
	}
}

func TestListHasStableCursorAndKindIsolation(t *testing.T) {
	s := newStore(t)
	for _, key := range []string{"alpha", "bravo", "charlie"} {
		if _, _, err := s.Create(context.Background(), domain.KindTag, tagDraft(key)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{Key: "alpha", Name: "alpha", Payload: map[string]json.RawMessage{"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"d"`), "base_unit": json.RawMessage(`"u"`), "default": json.RawMessage(`1`)}}); err != nil {
		t.Fatal(err)
	}
	first, err := s.List(context.Background(), domain.KindTag, "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" || first.Items[0].Key != "alpha" {
		t.Fatalf("first=%#v", first)
	}
	second, err := s.List(context.Background(), domain.KindTag, "", first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Key != "charlie" {
		t.Fatalf("second=%#v", second)
	}
	if _, err := s.List(context.Background(), domain.KindTag, "", "", 201); err == nil {
		t.Fatal("unbounded page accepted")
	}
}

func TestArchiveReferenceProtectionAndRevisionImmutability(t *testing.T) {
	s := newStore(t)
	tag, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	character := domain.EntityDraft{Key: "hero", Name: "hero", Payload: map[string]json.RawMessage{"attribute_values": json.RawMessage(`[]`), "skill_ids": json.RawMessage(`[]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`)}, TagIDs: []domain.ID{tag.ID}}
	if _, _, err := s.Create(context.Background(), domain.KindCharacter, character); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Delete(context.Background(), domain.KindTag, tag.ID, tag.EntityVersion); err == nil {
		t.Fatal("referenced archive accepted")
	} else {
		var referenced *ReferencedError
		if !errors.As(err, &referenced) || len(referenced.References) == 0 {
			t.Fatalf("archive error = %v", err)
		}
	}
	var revisionID string
	if err := s.db.QueryRow("SELECT id FROM config_revisions ORDER BY display_revision LIMIT 1").Scan(&revisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE config_revisions SET config_hash='mutated' WHERE id=?", revisionID); err == nil {
		t.Fatal("revision update accepted")
	}
	if _, err := s.db.Exec("DELETE FROM revision_entities WHERE revision_id=?", revisionID); err == nil {
		t.Fatal("manifest delete accepted")
	}
}

func TestProjectIdentitySurvivesMoveAndRejectsNewerSchema(t *testing.T) {
	r, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	original := filepath.Join(root, "one")
	moved := filepath.Join(root, "two")
	s, id, err := Create(context.Background(), original, r)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	reopened, movedID, err := Open(moved, r)
	if err != nil {
		t.Fatal(err)
	}
	if id != movedID {
		t.Fatalf("id changed: %s %s", id, movedID)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	newer := filepath.Join(root, "newer")
	if err := os.Mkdir(newer, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(newer, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE project_meta(id TEXT PRIMARY KEY, db_schema_version INTEGER NOT NULL, created_at TEXT NOT NULL); INSERT INTO project_meta VALUES(?,?,?)", id, 2, "2026-08-03T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, _, err := Open(newer, r); !errors.Is(err, ErrProjectInvalid) {
		t.Fatalf("newer schema = %v", err)
	}
}

func TestSaveRollbackAtEveryStage(t *testing.T) {
	for _, stage := range []string{"blob", "working", "indexes", "revision"} {
		t.Run(stage, func(t *testing.T) {
			s := newStore(t)
			s.failStage = func(at string) error {
				if at == stage {
					return errors.New("injected " + stage)
				}
				return nil
			}
			if _, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire")); err == nil {
				t.Fatal("save unexpectedly succeeded")
			}
			for _, table := range []string{"entity_blobs", "working_entities", "entity_references", "entity_tags", "config_revisions", "revision_entities"} {
				var count int
				if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("%s left %d rows", table, count)
				}
			}
		})
	}
}

func TestDerivedIndexesRebuildFromStoredBlob(t *testing.T) {
	s := newStore(t)
	tag, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	draft := domain.EntityDraft{Key: "hero", Name: "hero", TagIDs: []domain.ID{tag.ID}, Payload: map[string]json.RawMessage{"attribute_values": json.RawMessage(`[]`), "skill_ids": json.RawMessage(`[]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`)}}
	entity, _, err := s.Create(context.Background(), domain.KindCharacter, draft)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := s.db.QueryRow(`SELECT b.json FROM working_entities w JOIN entity_blobs b ON b.hash=w.blob_hash WHERE w.id=?`, entity.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var restored domain.Entity
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	refs, tags := domain.ExtractIndexes(restored)
	for table, want := range map[string]int{"entity_references": len(refs), "entity_tags": len(tags)} {
		var got int
		if err := s.db.QueryRow("SELECT count(*) FROM "+table+" WHERE "+map[string]string{"entity_references": "source_entity_id", "entity_tags": "entity_id"}[table]+"=?", entity.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s=%d want %d", table, got, want)
		}
	}
}

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
