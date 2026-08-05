package sqlite

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/validation"
)

func TestRebuildOnlyChangesSelectedRevisionDerivedRows(t *testing.T) {
	store := newStore(t)
	entity, revision, err := store.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{Key: "power", Name: "Power", Payload: map[string]json.RawMessage{"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"damage"`), "base_unit": json.RawMessage(`"damage_point"`), "default": json.RawMessage(`"1"`)}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.MaterializeValidationSource(context.Background(), "revision", revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	registry, _ := formula.V1Registry()
	result, err := store.RebuildRevisionDerived(context.Background(), revision.ID, registry, formula.SymbolTable{})
	if err != nil || result.ReferenceCount != 0 {
		t.Fatalf("%#v %v", result, err)
	}
	after, err := store.MaterializeValidationSource(context.Background(), "revision", revision.ID)
	if err != nil || after.Entities[0].ID != entity.ID || string(after.Entities[0].Payload["default"]) != string(before.Entities[0].Payload["default"]) {
		t.Fatalf("%#v %v", after, err)
	}
}

func TestRebuildRestoresEquivalentFormulaIndexesAndFullResult(t *testing.T) {
	store := newStore(t)
	attribute, _, err := store.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{Key: "power", Name: "Power", Payload: map[string]json.RawMessage{"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"damage"`), "base_unit": json.RawMessage(`"damage_point"`), "default": json.RawMessage(`"1"`)}})
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := store.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Hero", Payload: map[string]json.RawMessage{
		"attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(attribute.ID) + `","expression":"${scenario:level}"}]`), "skill_ids": json.RawMessage(`[]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	beforeSource, err := store.MaterializeValidationSource(context.Background(), validation.SourceRevision, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstRun, err := store.RunValidation(context.Background(), validation.SourceRevision, revision.ID, validation.ScopeFull)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := formula.V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	scalar, ok := registry.Unit("scalar")
	if !ok {
		t.Fatal("scalar unit missing")
	}
	symbols := formula.SymbolTable{"scenario:level": {ValueType: formula.DecimalType, Unit: scalar}}
	if _, err = store.RebuildRevisionDerived(context.Background(), revision.ID, registry, symbols); err != nil {
		t.Fatal(err)
	}
	firstIndexes := revisionDerivedRows(t, store, revision.ID)
	if len(firstIndexes) == 0 {
		t.Fatal("formula rebuild did not create an AST index")
	}
	if _, err = store.db.Exec(`DELETE FROM compiled_ast WHERE revision_id=?`, revision.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DELETE FROM formula_index WHERE revision_id=?`, revision.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`DELETE FROM revision_references WHERE revision_id=?`, revision.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RebuildRevisionDerived(context.Background(), revision.ID, registry, symbols); err != nil {
		t.Fatal(err)
	}
	if rebuilt := revisionDerivedRows(t, store, revision.ID); !reflect.DeepEqual(firstIndexes, rebuilt) {
		t.Fatalf("derived rows changed after rebuild\nfirst=%#v\nrebuilt=%#v", firstIndexes, rebuilt)
	}
	afterSource, err := store.MaterializeValidationSource(context.Background(), validation.SourceRevision, revision.ID)
	if err != nil || beforeSource.Source.InputHash != afterSource.Source.InputHash || !reflect.DeepEqual(beforeSource.Entities, afterSource.Entities) {
		t.Fatalf("revision fact changed before=%#v after=%#v err=%v", beforeSource, afterSource, err)
	}
	secondRun, err := store.RunValidation(context.Background(), validation.SourceRevision, revision.ID, validation.ScopeFull)
	if err != nil || firstRun.Run.ResultHash != secondRun.Run.ResultHash || firstRun.Run.ID == secondRun.Run.ID {
		t.Fatalf("runs were not immutable equivalent\nfirst=%#v\nsecond=%#v\nerr=%v", firstRun.Run, secondRun.Run, err)
	}
}

func revisionDerivedRows(t *testing.T, store *Store, revisionID domain.ID) []string {
	t.Helper()
	rows, err := store.db.Query(`SELECT 'ast',entity_id,field_path,ast_hash FROM compiled_ast WHERE revision_id=? ORDER BY entity_id,field_path`, revisionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	values := []string{}
	for rows.Next() {
		var table, entityID, path, hash string
		if err = rows.Scan(&table, &entityID, &path, &hash); err != nil {
			t.Fatal(err)
		}
		values = append(values, table+"|"+entityID+"|"+path+"|"+hash)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT 'formula',source_entity_id,field_path,scope || ':' || symbol || ':' || CAST(span_start AS TEXT) || ':' || CAST(span_end AS TEXT) FROM formula_index WHERE revision_id=? ORDER BY source_entity_id,field_path,read_ordinal`,
		`SELECT 'reference',source_entity_id,field_path,expected_kind || ':' || target_entity_id || ':' || CAST(ordinal AS TEXT) FROM revision_references WHERE revision_id=? ORDER BY source_entity_id,field_path,ordinal`,
	} {
		rows, err = store.db.Query(query, revisionID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var table, entityID, path, value string
			if err = rows.Scan(&table, &entityID, &path, &value); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			values = append(values, table+"|"+entityID+"|"+path+"|"+value)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return values
}
