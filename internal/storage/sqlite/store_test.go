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
	"github.com/zouyi/eco-guardian/internal/validation"
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
	if _, _, err := s.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{Key: "alpha", Name: "alpha", Payload: map[string]json.RawMessage{"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"d"`), "base_unit": json.RawMessage(`"u"`), "default": json.RawMessage(`"1"`)}}); err != nil {
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
	if _, err = db.Exec("CREATE TABLE project_meta(id TEXT PRIMARY KEY, db_schema_version INTEGER NOT NULL, created_at TEXT NOT NULL); INSERT INTO project_meta VALUES(?,?,?)", id, 3, "2026-08-03T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, _, err := Open(newer, r); !errors.Is(err, ErrProjectInvalid) {
		t.Fatalf("newer schema = %v", err)
	}
}

func TestSaveRollbackAtEveryStage(t *testing.T) {
	for _, stage := range []string{"blob", "working", "indexes", "revision", "derived"} {
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
			for _, table := range []string{"entity_blobs", "working_entities", "entity_references", "entity_tags", "config_revisions", "revision_entities", "compiled_ast", "formula_index", "revision_references"} {
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

func TestPatchBaseValidationRejectsBeforeWriteStages(t *testing.T) {
	s := newStore(t)
	entity, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	s.failStage = func(string) error { return errors.New("write transaction should not start") }
	_, _, err = s.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"key": json.RawMessage(`"INVALID KEY"`)})
	var validation ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("patch error=%v", err)
	}
	got, err := s.Get(context.Background(), domain.KindTag, entity.ID)
	if err != nil || got.Key != "fire" || got.EntityVersion != entity.EntityVersion {
		t.Fatalf("entity changed=%#v err=%v", got, err)
	}
}

func TestLocalPreflightDetectsChangedManifestAndDependencies(t *testing.T) {
	s := newStore(t)
	tag, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	output, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	prospective, err := domain.NewEntity(domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", TagIDs: []domain.ID{tag.ID}, Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(output) + `","expression":"min(1, 2)"}]`),
		"skill_ids":        json.RawMessage(`[]`),
		"item_ids":         json.RawMessage(`[]`),
		"rule_blocks":      json.RawMessage(`[]`),
	}}, s.now())
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := s.preflightLocal(context.Background(), prospective)
	if err != nil || preflight.workingHash == "" || len(preflight.dependencies) != 2 {
		t.Fatalf("preflight=%#v err=%v", preflight, err)
	}
	if _, _, err = s.Patch(context.Background(), domain.KindTag, tag.ID, tag.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"Flame"`)}); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fresh, err := s.recheckLocalPreflightTx(context.Background(), tx, preflight)
	if err != nil || fresh {
		t.Fatalf("recheck fresh=%v err=%v", fresh, err)
	}
}

func TestSemanticBlockCreatesLocalRevisionAndImmutableReport(t *testing.T) {
	s := newStore(t)
	missing, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	entity, revision, err := s.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[]`), "skill_ids": json.RawMessage(`["` + string(missing) + `"]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if revision.Validation == nil || revision.Validation.Scope != "LOCAL" || revision.Validation.Block != 1 || revision.Validation.RunID == "" {
		t.Fatalf("local summary=%#v", revision.Validation)
	}
	report, err := s.GetValidationReport(context.Background(), string(revision.Validation.RunID))
	if err != nil || report.Run.Scope != validation.ScopeLocal || report.Run.Source.RevisionID != revision.ID || len(report.Issues) != 1 || report.Issues[0].Code != "REFERENCE_NOT_FOUND" || report.Issues[0].EntityID != entity.ID {
		t.Fatalf("report=%#v err=%v", report, err)
	}
}

func TestBlockRevisionLocalReportSurvivesRestart(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, _, err := Create(context.Background(), dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := store.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[]`), "skill_ids": json.RawMessage(`["` + string(missing) + `"]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`),
	}})
	if err != nil || revision.Validation == nil || revision.Validation.Block != 1 {
		t.Fatalf("revision=%#v err=%v", revision, err)
	}
	runID := revision.Validation.RunID
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := Open(dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.GetValidationReport(context.Background(), string(runID))
	if err != nil || recovered.Run.Scope != validation.ScopeLocal || recovered.Run.Source.RevisionID != revision.ID || recovered.Run.Summary.Block != 1 || len(recovered.Issues) != 1 {
		t.Fatalf("recovered=%#v err=%v", recovered, err)
	}
}

func TestLocalValidationFailureRollsBackTheWholeSave(t *testing.T) {
	for _, stage := range []string{"validation_run", "validation_issue"} {
		t.Run(stage, func(t *testing.T) {
			s := newStore(t)
			s.failStage = func(at string) error {
				if at == stage {
					return errors.New("injected " + stage)
				}
				return nil
			}
			var err error
			if stage == "validation_run" {
				_, _, err = s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
			} else {
				attribute, idErr := domain.NewID()
				if idErr != nil {
					t.Fatal(idErr)
				}
				_, _, err = s.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", Payload: map[string]json.RawMessage{
					"attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(attribute) + `","expression":"@"}]`), "skill_ids": json.RawMessage(`[]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`),
				}})
			}
			if err == nil {
				t.Fatal("save unexpectedly succeeded")
			}
			for _, table := range []string{"entity_blobs", "working_entities", "config_revisions", "revision_entities", "validation_runs", "validation_issues"} {
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

func TestLocalSavePersistsRevisionDerivedIndexes(t *testing.T) {
	s := newStore(t)
	tag, _, err := s.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	attribute, _, err := s.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{Key: "power", Name: "Power", Payload: map[string]json.RawMessage{
		"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"scalar"`), "base_unit": json.RawMessage(`"scalar"`), "default": json.RawMessage(`"0"`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := s.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", TagIDs: []domain.ID{tag.ID}, Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(attribute.ID) + `","expression":"min(1, 2)"}]`),
		"skill_ids":        json.RawMessage(`[]`),
		"item_ids":         json.RawMessage(`[]`),
		"rule_blocks":      json.RawMessage(`[]`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"compiled_ast": 1, "formula_index": 0, "revision_references": 2} {
		var got int
		if err := s.db.QueryRow("SELECT count(*) FROM "+table+" WHERE revision_id=?", revision.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s=%d want %d", table, got, want)
		}
	}
}

func TestDerivedIndexFailureRollsBackWholeSave(t *testing.T) {
	s := newStore(t)
	output, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	s.failStage = func(at string) error {
		if at == "derived" {
			return errors.New("injected derived")
		}
		return nil
	}
	_, _, err = s.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(output) + `","expression":"min(1, 2)"}]`),
		"skill_ids":        json.RawMessage(`[]`),
		"item_ids":         json.RawMessage(`[]`),
		"rule_blocks":      json.RawMessage(`[]`),
	}})
	if err == nil {
		t.Fatal("save unexpectedly succeeded")
	}
	for _, table := range []string{"entity_blobs", "working_entities", "config_revisions", "revision_entities", "compiled_ast", "formula_index", "revision_references", "validation_runs", "validation_issues"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s left %d rows", table, count)
		}
	}
}

func TestInvalidFormulaSavesBlockWithoutCompiledAST(t *testing.T) {
	s := newStore(t)
	output, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := s.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(output) + `","expression":"@"}]`),
		"skill_ids":        json.RawMessage(`[]`),
		"item_ids":         json.RawMessage(`[]`),
		"rule_blocks":      json.RawMessage(`[]`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if revision.Validation == nil || revision.Validation.Block == 0 {
		t.Fatalf("expected local BLOCK summary, got %#v", revision.Validation)
	}
	var astCount, refCount int
	if err := s.db.QueryRow("SELECT count(*) FROM compiled_ast WHERE revision_id=?", revision.ID).Scan(&astCount); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM revision_references WHERE revision_id=?", revision.ID).Scan(&refCount); err != nil {
		t.Fatal(err)
	}
	if astCount != 0 || refCount != 1 {
		t.Fatalf("compiled_ast=%d revision_references=%d", astCount, refCount)
	}
}

func TestLocalSaveDefersGlobalRuleSafetyToFullValidation(t *testing.T) {
	s := newStore(t)
	_, revision, err := s.Create(context.Background(), domain.KindEffect, domain.EntityDraft{Key: "burn", Name: "Burn", Payload: map[string]json.RawMessage{
		"duration":       json.RawMessage(`"0"`),
		"modifiers":      json.RawMessage(`[]`),
		"trigger_blocks": json.RawMessage(`[]`),
		"stack_rule":     json.RawMessage(`{"operation":"Add","max_stacks":"0","refresh_policy":"refresh"}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if revision.Validation == nil || revision.Validation.Block != 0 {
		t.Fatalf("LOCAL must not run global stack safety: %#v", revision.Validation)
	}
	report, err := s.RunValidation(context.Background(), validation.SourceRevision, revision.ID, validation.ScopeFull)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, issue := range report.Issues {
		codes[issue.Code] = true
	}
	if !codes["STACK_PRIORITY_MISSING"] || !codes["STACK_UNBOUNDED"] {
		t.Fatalf("FULL issues=%#v", report.Issues)
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
