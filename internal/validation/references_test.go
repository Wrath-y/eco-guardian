package validation

import (
	"encoding/json"
	"github.com/zouyi/eco-guardian/internal/domain"
	"testing"
)

func referenceID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func TestKnownWalkerUsesPointersAndSkipsExtensions(t *testing.T) {
	tag := referenceID(t)
	character := referenceID(t)
	attribute := referenceID(t)
	entities := []domain.Entity{{ID: character, Kind: domain.KindCharacter, Status: domain.StatusActive, Payload: map[string]json.RawMessage{"skill_ids": json.RawMessage(`[]`), "attribute_values": json.RawMessage(`[{"output_attribute_id":"` + string(attribute) + `","expression":"1"}]`), "nested": json.RawMessage(`{"tag_id":"` + string(tag) + `"}`)}, Extensions: map[string]json.RawMessage{"vendor/rule": json.RawMessage(`{"effect_ids":["` + string(tag) + `"]}`)}}, {ID: tag, Kind: domain.KindTag, Status: domain.StatusActive}}
	entities[0].Payload["attribute_values"] = json.RawMessage(`[{"output_attribute_id":"` + string(attribute) + `","expression":"${self:甲}"}]`)
	references, formulas := WalkKnownSchema(entities)
	if len(references) != 2 || references[1].FieldPath != "/payload/nested/tag_id" {
		t.Fatalf("references=%#v", references)
	}
	if len(formulas) != 1 || formulas[0].FieldPath != "/payload/attribute_values/0/expression" || formulas[0].Span.EndByte != len(formulas[0].Expression) || len(formulas[0].Scopes) != 1 || formulas[0].Scopes[0] != "self" {
		t.Fatalf("formulas=%#v", formulas)
	}
}
func TestReferenceValidationUsesSnapshotIDsNotNames(t *testing.T) {
	source, target := referenceID(t), referenceID(t)
	entities := []domain.Entity{{ID: source, Kind: domain.KindCharacter, Status: domain.StatusActive}, {ID: target, Kind: domain.KindEffect, Status: domain.StatusArchived, Name: "renamed"}}
	references := []ReferenceTuple{{SourceID: source, FieldPath: "/payload/effect_ids/0", Ordinal: 0, ExpectedKind: domain.KindEffect, TargetID: target}, {SourceID: source, FieldPath: "/payload/effect_ids/1", Ordinal: 1, ExpectedKind: domain.KindEffect, TargetID: referenceID(t)}}
	issues := ValidateReferences(entities, references)
	if len(issues) != 2 || issues[0].Code != "REFERENCE_TARGET_INACTIVE" || issues[1].Code != "REFERENCE_NOT_FOUND" {
		t.Fatalf("%#v", issues)
	}
}
