package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	root "github.com/zouyi/eco-guardian"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

func TestCompletedValidationReportsAreImmutableAndExact(t *testing.T) {
	store := newStore(t)
	entity, revision, err := store.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{Key: "power", Name: "Power", Payload: map[string]json.RawMessage{"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"damage"`), "base_unit": json.RawMessage(`"damage_point"`), "default": json.RawMessage(`"1"`)}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := validation.NewSource(validation.SourceRevision, revision.ID, revision.ConfigHash)
	if err != nil {
		t.Fatal(err)
	}
	versions := validation.VersionManifest{Schema: "v1", DSL: "dsl-v1", Registry: "registry-v1", NumericPolicy: "numeric-v1"}
	issue, err := validation.NewIssue(validation.SeverityBlock, "REFERENCE_NOT_FOUND", entity.ID, "/payload/effect_ids/0", nil, nil, nil, map[string]string{"target": "missing"}, "registry-v1")
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := domain.NewID()
	run, err := validation.NewCompletedRun(runID, source, validation.ScopeFull, versions, []validation.Issue{issue}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.InsertCompletedValidationRun(context.Background(), run, []validation.Issue{issue}); err != nil {
		t.Fatal(err)
	}
	report, err := store.GetValidationReport(context.Background(), string(runID))
	if err != nil || len(report.Issues) != 1 || report.Run.ResultHash != run.ResultHash {
		t.Fatalf("%#v %v", report, err)
	}
	matched, ok, err := store.FindMatchingFullRun(context.Background(), revision.ID, revision.ConfigHash, versions)
	if err != nil || !ok || matched.ID != runID {
		t.Fatalf("%#v %v %v", matched, ok, err)
	}
	if _, err = store.db.Exec(`UPDATE validation_runs SET result_hash='bad' WHERE id=?`, runID); err == nil {
		t.Fatal("completed run mutated")
	}
	if _, err = store.db.Exec(`DELETE FROM validation_runs WHERE id=?`, runID); err == nil {
		t.Fatal("completed run deleted")
	}
	if _, err = store.db.Exec(`UPDATE validation_issues SET code='changed' WHERE run_id=?`, runID); err == nil {
		t.Fatal("completed issue mutated")
	}
	if _, err = store.db.Exec(`DELETE FROM validation_issues WHERE run_id=?`, runID); err == nil {
		t.Fatal("completed issue deleted")
	}
}

func TestFullValidationWarningCodesReturnsOnlyStableCodes(t *testing.T) {
	store := newStore(t)
	run, issues := validationRunFixture(t, store)
	newIssue := func(code, path string, severity validation.Severity) validation.Issue {
		issue, err := validation.NewIssue(validation.SeverityBlock, code, issues[0].EntityID, path, nil, nil, nil, nil, run.Versions.Registry)
		if err != nil {
			t.Fatal(err)
		}
		issue.Severity = severity
		return issue
	}
	warnings := []validation.Issue{
		newIssue("STATIC_FORMULA_CYCLE", "/payload/z", validation.SeverityWarning),
		newIssue("REFERENCE_NOT_FOUND", "/payload/a", validation.SeverityWarning),
		newIssue("STATIC_FORMULA_CYCLE", "/payload/b", validation.SeverityWarning),
		newIssue("REFERENCE_NOT_FOUND", "/payload/c", validation.SeverityInfo),
	}
	run, err := validation.NewCompletedRun(run.ID, run.Source, validation.ScopeFull, run.Versions, warnings, run.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.InsertCompletedValidationRun(context.Background(), run, warnings); err != nil {
		t.Fatal(err)
	}
	codes, err := store.FullValidationWarningCodes(context.Background(), run.Source.RevisionID, run.Source.InputHash, run.Versions)
	if err != nil || !reflect.DeepEqual(codes, []string{"REFERENCE_NOT_FOUND", "STATIC_FORMULA_CYCLE"}) {
		t.Fatalf("codes=%#v err=%v", codes, err)
	}
}

func TestValidationReportBulkInsertRollsBackAtEveryStage(t *testing.T) {
	for _, stage := range []string{"validation_run", "validation_issue"} {
		t.Run(stage, func(t *testing.T) {
			store := newStore(t)
			run, issues := validationRunFixture(t, store)
			before := map[string]int{}
			for _, table := range []string{"validation_runs", "validation_issues"} {
				var count int
				if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				before[table] = count
			}
			store.failStage = func(at string) error {
				if at == stage {
					return errors.New("injected " + stage)
				}
				return nil
			}
			if err := store.InsertCompletedValidationRun(context.Background(), run, issues); err == nil {
				t.Fatal("insert unexpectedly succeeded")
			}
			for _, table := range []string{"validation_runs", "validation_issues"} {
				var count int
				if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != before[table] {
					t.Fatalf("%s changed from %d to %d rows", table, before[table], count)
				}
			}
		})
	}
}

func validationRunFixture(t *testing.T, store *Store) (validation.ValidationRun, []validation.Issue) {
	t.Helper()
	entity, revision, err := store.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{Key: "power", Name: "Power", Payload: map[string]json.RawMessage{"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"damage"`), "base_unit": json.RawMessage(`"damage_point"`), "default": json.RawMessage(`"1"`)}})
	if err != nil {
		t.Fatal(err)
	}
	source, err := validation.NewSource(validation.SourceRevision, revision.ID, revision.ConfigHash)
	if err != nil {
		t.Fatal(err)
	}
	versions := validation.VersionManifest{Schema: "v1", DSL: "dsl-v1", Registry: "registry-v1", NumericPolicy: "numeric-v1"}
	issue, err := validation.NewIssue(validation.SeverityBlock, "REFERENCE_NOT_FOUND", entity.ID, "/payload/effect_ids/0", nil, nil, nil, map[string]string{"target": "missing"}, "registry-v1")
	if err != nil {
		t.Fatal(err)
	}
	runID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	run, err := validation.NewCompletedRun(runID, source, validation.ScopeFull, versions, []validation.Issue{issue}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return run, []validation.Issue{issue}
}

func TestValidationSQLConstraintsAndOrderedReportRows(t *testing.T) {
	store := newStore(t)
	run, issues := validationRunFixture(t, store)
	second, err := validation.NewIssue(validation.SeverityBlock, "REFERENCE_NOT_FOUND", issues[0].EntityID, "/payload/a", nil, nil, nil, map[string]string{"target": "another"}, "registry-v1")
	if err != nil {
		t.Fatal(err)
	}
	run, err = validation.NewCompletedRun(run.ID, run.Source, run.Scope, run.Versions, []validation.Issue{issues[0], second}, run.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.InsertCompletedValidationRun(context.Background(), run, []validation.Issue{issues[0], second}); err != nil {
		t.Fatal(err)
	}
	report, err := store.GetValidationReport(context.Background(), string(run.ID))
	if err != nil || len(report.Issues) != 2 || report.Issues[0].FieldPath != "/payload/a" || report.Issues[1].FieldPath != "/payload/effect_ids/0" {
		t.Fatalf("ordered report=%#v err=%v", report, err)
	}
	if _, err = store.db.Exec(`INSERT INTO formula_index(revision_id,source_entity_id,field_path,read_ordinal,output_attribute_id,scope,symbol,span_start,span_end,value_type,unit) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, run.Source.RevisionID, issues[0].EntityID, "/payload/x", 0, issues[0].EntityID, "self", "x", 3, 3, "", ""); err == nil {
		t.Fatal("formula span constraint accepted")
	}
	if _, err = store.db.Exec(`INSERT INTO validation_runs(id,source_kind,source_input_hash,scope,version_manifest_hash,version_manifest,status,error_count,block_count,warning_count,info_count,result_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, mustID(t), "working", run.Source.InputHash, "INVALID", "0123456789012345678901234567890123456789012345678901234567890123", "{}", "completed", 0, 0, 0, 0, run.ResultHash, run.CreatedAt); err == nil {
		t.Fatal("validation scope constraint accepted")
	}
}

func TestValidationReportRestartAndUnknownExtensionExclusion(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	store, _, err := Create(context.Background(), dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := store.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "fire", Name: "Fire", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}, Extensions: map[string]json.RawMessage{"vendor.example/rule": json.RawMessage(`{"effect_ids":["not-a-known-reference"]}`)}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := store.RunValidation(context.Background(), validation.SourceRevision, revision.ID, validation.ScopeFull)
	if err != nil || len(report.Issues) != 0 {
		t.Fatalf("extension result=%#v err=%v", report, err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := Open(dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.GetValidationReport(context.Background(), string(report.Run.ID))
	if err != nil || !reflect.DeepEqual(recovered, report) {
		t.Fatalf("recovered=%#v original=%#v err=%v", recovered, report, err)
	}
	snapshot, err := reopened.MaterializeValidationSource(context.Background(), validation.SourceRevision, revision.ID)
	if err != nil || string(snapshot.Entities[0].Extensions["vendor.example/rule"]) != `{"effect_ids":["not-a-known-reference"]}` {
		t.Fatalf("extension was not preserved: %#v err=%v", snapshot, err)
	}
}

func TestRepeatedFullRunsHaveDistinctIDsAndCanonicalResults(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), domain.KindTag, tagDraft("fire"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.RunValidation(context.Background(), validation.SourceRevision, revision.ID, validation.ScopeFull)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.RunValidation(context.Background(), validation.SourceRevision, revision.ID, validation.ScopeFull)
	if err != nil {
		t.Fatal(err)
	}
	if first.Run.ID == second.Run.ID || first.Run.ResultHash != second.Run.ResultHash || first.Run.Summary != second.Run.Summary || !reflect.DeepEqual(first.Issues, second.Issues) || first.Run.Source != second.Run.Source || first.Run.Versions != second.Run.Versions {
		t.Fatalf("first=%#v\nsecond=%#v", first, second)
	}
}

func TestOpenMigratesV1DatabaseWithValidationConstraints(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, databaseName))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := root.Assets.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	id := mustID(t)
	if _, err = db.Exec(string(initial)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO project_meta(id,db_schema_version,created_at) VALUES(?,?,?)`, id, 1, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	store, openedID, err := Open(dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if openedID != id {
		t.Fatalf("project id changed: %s != %s", openedID, id)
	}
	var version int
	if err = store.db.QueryRow(`SELECT db_schema_version FROM project_meta WHERE id=?`, id).Scan(&version); err != nil || version != currentSchemaVersion {
		t.Fatalf("schema version=%d err=%v", version, err)
	}
	for _, table := range []string{"compiled_ast", "formula_index", "revision_references", "validation_runs", "validation_issues", "revision_metadata", "release_policies", "jobs", "job_events", "release_intents", "releases", "active_release_pointer"} {
		var found string
		if err = store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil || found != table {
			t.Fatalf("missing migrated table %q: %v", table, err)
		}
	}
}

func mustID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
