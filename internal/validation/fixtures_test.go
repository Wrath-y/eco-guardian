package validation

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

func TestReferenceFixturesKeepArrayPositionsAndIgnoreExtensions(t *testing.T) {
	source, active, archived, wrongKind := referenceID(t), referenceID(t), referenceID(t), referenceID(t)
	entities := []domain.Entity{
		{ID: source, Kind: domain.KindItem, Status: domain.StatusActive, Name: "renamed source", Payload: map[string]json.RawMessage{
			"effect_ids": json.RawMessage(`["` + string(active) + `","` + string(active) + `","` + string(archived) + `","` + string(wrongKind) + `"]`),
		}, Extensions: map[string]json.RawMessage{"vendor/rules": json.RawMessage(`{"effect_ids":["missing"]}`)}},
		{ID: active, Kind: domain.KindEffect, Status: domain.StatusActive, Name: "renamed effect"},
		{ID: archived, Kind: domain.KindEffect, Status: domain.StatusArchived},
		{ID: wrongKind, Kind: domain.KindTag, Status: domain.StatusActive},
	}
	references, formulas := WalkKnownSchema(entities)
	if len(formulas) != 0 || len(references) != 4 {
		t.Fatalf("references=%#v formulas=%#v", references, formulas)
	}
	for index, reference := range references {
		if reference.FieldPath != "/payload/effect_ids/"+strconv.Itoa(index) || reference.Ordinal != index {
			t.Fatalf("reference %d = %#v", index, reference)
		}
	}
	findings := ValidateReferences(entities, references)
	if len(findings) != 2 || findings[0].Code != "REFERENCE_TARGET_INACTIVE" || findings[0].Tuple.Ordinal != 2 || findings[1].Code != "REFERENCE_KIND_MISMATCH" || findings[1].Tuple.Ordinal != 3 {
		t.Fatalf("findings=%#v", findings)
	}
}

func TestFormulaFixturesKeepMalformedAndUnsupportedScopeSpans(t *testing.T) {
	registry, err := formula.V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	id := referenceID(t)
	output := referenceID(t)
	bindings := []FormulaTuple{
		{SourceID: id, FieldPath: "/payload/attribute_values/0/expression", Ordinal: 0, OutputAttributeID: output, Expression: "@"},
		{SourceID: id, FieldPath: "/payload/attribute_values/1/expression", Ordinal: 1, OutputAttributeID: output, Expression: "${unknown:x}"},
	}
	_, issues := CompileFormulaBindings(bindings, registry, formula.SymbolTable{})
	var malformed, unsupported bool
	for _, issue := range issues {
		if issue.Code == "FORMULA_SYNTAX_INVALID" && issue.FieldPath == bindings[0].FieldPath && issue.Span == (formula.Span{StartByte: 0, EndByte: 1}) {
			malformed = true
		}
		if issue.Code == "FORMULA_UNKNOWN_VARIABLE" && issue.FieldPath == bindings[1].FieldPath && issue.Span == (formula.Span{StartByte: 0, EndByte: len(bindings[1].Expression)}) {
			unsupported = true
		}
	}
	if !malformed || !unsupported {
		t.Fatalf("malformed=%t unsupported=%t issues=%#v", malformed, unsupported, issues)
	}
}
