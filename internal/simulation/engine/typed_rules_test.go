package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/rules/materialization"
	"github.com/zouyi/eco-guardian/internal/validation"
)

func TestTypedRuleAdapterEvaluatesMaterializedASTAndResolvesStableTargets(t *testing.T) {
	registry, err := formula.V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	project, revision, validationRun := engineRuleID(t), engineRuleID(t), engineRuleID(t)
	attribute, tag, effect, skill := engineRuleID(t), engineRuleID(t), engineRuleID(t), engineRuleID(t)
	self, source, targetA, targetB := engineRuleID(t), engineRuleID(t), engineRuleID(t), engineRuleID(t)
	parsed := formula.Parse("${self:power} + ${scenario:bonus}", registry)
	if len(parsed.Errors) != 0 {
		t.Fatalf("parse: %+v", parsed.Errors)
	}
	ast, err := parsed.AST.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	astHash, err := parsed.AST.Hash()
	if err != nil {
		t.Fatal(err)
	}
	damage, _ := registry.Unit("damage_point")
	formulas, diagnostics := validation.CompileFormulaBindings([]validation.FormulaTuple{{SourceID: skill, FieldPath: "/payload/costs/0/expression", OutputAttributeID: attribute, Expression: "${self:power} + ${scenario:bonus}"}}, registry, formula.SymbolTable{"self:power": {ValueType: formula.DecimalType, Unit: damage}, "scenario:bonus": {ValueType: formula.DecimalType, Unit: damage}})
	if len(diagnostics) != 0 {
		t.Fatalf("formula diagnostics: %+v", diagnostics)
	}
	if string(formulas[0].AST) != string(ast) || formulas[0].ASTHash != astHash {
		t.Fatal("validation did not preserve typed AST")
	}
	entity := func(id domain.ID, kind domain.EntityKind, payload map[string]json.RawMessage, tags ...domain.ID) domain.Entity {
		return domain.Entity{ID: id, Kind: kind, Status: domain.StatusActive, SchemaVersion: 1, Payload: payload, Extensions: map[string]json.RawMessage{}, TagIDs: tags}
	}
	rules, err := materialization.Build(materialization.Source{
		ProjectID: project, RevisionID: revision, ConfigHash: strings.Repeat("a", 64),
		Certification: materialization.Certification{RunID: validationRun, ResultHash: strings.Repeat("b", 64), Versions: validation.VersionManifest{Schema: "schema-v1", DSL: formula.DSLVersion, Registry: "registry-v1", NumericPolicy: "numeric-v1"}},
		Entities: []domain.Entity{
			entity(attribute, domain.KindAttribute, map[string]json.RawMessage{}),
			entity(tag, domain.KindTag, map[string]json.RawMessage{}),
			entity(effect, domain.KindEffect, map[string]json.RawMessage{}),
			entity(skill, domain.KindSkill, map[string]json.RawMessage{
				"costs":       json.RawMessage(`[{"output_attribute_id":"` + string(attribute) + `","expression":"${self:power} + ${scenario:bonus}"}]`),
				"rule_blocks": json.RawMessage(`[{"event":"on_use","target":{"type":"targets_with_tag","tag_id":"` + string(tag) + `"},"effect_ids":["` + string(effect) + `"]}]`),
			}),
		}, Formulas: formulas,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewTypedRuleAdapter(rules, registry, []EventRegistration{{EffectID: effect, EventID: "effect-applied-v1", EventVersion: "v1", EvaluatorID: "effect-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	value := engineDecimal(t, "2")
	bonus := engineDecimal(t, "1")
	context := RuleContext{SelfID: self, SourceID: source, TargetID: targetA, Entities: map[domain.ID]RuleEntity{
		self:    {Attributes: map[string]formula.Result{"power": {Type: formula.Type{ValueType: formula.DecimalType, Unit: damage}, Decimal: &value}}},
		source:  {Attributes: map[string]formula.Result{}},
		targetA: {Attributes: map[string]formula.Result{}, Tags: map[domain.ID]struct{}{tag: {}}},
		targetB: {Attributes: map[string]formula.Result{}, Tags: map[domain.ID]struct{}{tag: {}}},
	}, Scenario: formula.RuntimeTable{"scenario:bonus": {Type: formula.Type{ValueType: formula.DecimalType, Unit: damage}, Decimal: &bonus}}}
	bound, err := adapter.EvaluateFormulas(context)
	if err != nil || len(bound) != 1 || bound[0].AttributeID != attribute || bound[0].Value.Decimal == nil || bound[0].Value.Decimal.String() != "3" {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
	effects, err := adapter.TriggeredEffects(context, "on_use")
	if err != nil || len(effects) != 2 {
		t.Fatalf("effects=%+v err=%v", effects, err)
	}
	if effects[0].TargetID > effects[1].TargetID || effects[0].EventID != "effect-applied-v1" || effects[0].EvaluatorID != "effect-v1" {
		t.Fatalf("effects not deterministic or registered: %+v", effects)
	}
}

func TestTypedRuleAdapterRejectsOpaqueTriggerCondition(t *testing.T) {
	rules, registry, _, _ := typedRuleFixture(t)
	rules.TriggerRules[0].Condition = "${self:power} > 0"
	if _, err := NewTypedRuleAdapter(rules, registry, nil); err == nil {
		t.Fatal("expected opaque condition to be rejected rather than reparsed")
	}
}

func TestTypedRuleAdapterUsesDecimalModifiersAndTypedStackInputs(t *testing.T) {
	rules, registry, attribute, effect := typedRuleFixture(t)
	adapter, err := NewTypedRuleAdapter(rules, registry, []EventRegistration{{EffectID: effect, EventID: "effect-v1", EventVersion: "v1", EvaluatorID: "effect-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	damage, _ := registry.Unit("damage_point")
	initial := engineDecimal(t, "2")
	changes, err := adapter.ApplyModifiers(RuleEntity{Attributes: map[string]formula.Result{string(attribute): {Type: formula.Type{ValueType: formula.DecimalType, Unit: damage}, Decimal: &initial}}})
	if err != nil || len(changes) != 1 || changes[0].Value.Decimal == nil || changes[0].Value.Decimal.String() != "3" {
		t.Fatalf("changes=%+v err=%v", changes, err)
	}
	stacks, err := adapter.StackInstructions()
	if err != nil || len(stacks) != 1 || stacks[0].MaxStacks.String() != "2" || stacks[0].Cap == nil || stacks[0].Cap.String() != "10" {
		t.Fatalf("stacks=%+v err=%v", stacks, err)
	}
}

func TestTypedRuleModifierPreservesUnitsAndUsesScalarRatioForMultiply(t *testing.T) {
	registry, err := formula.V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	damage, _ := registry.Unit("damage_point")
	three := engineDecimal(t, "3")
	two := engineDecimal(t, "2")
	current := formula.Result{Type: formula.Type{ValueType: formula.DecimalType, Unit: damage}, Decimal: &three}
	scalar, _ := registry.Unit("scalar")
	multiplier := formula.Result{Type: formula.Type{ValueType: formula.DecimalType, Unit: scalar}, Decimal: &two}
	next, err := applyModifier("Multiply", current, multiplier, registry)
	if err != nil || next.Decimal == nil || next.Decimal.String() != "6" || next.Type.Unit != damage {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	if _, err = applyModifier("Add", current, multiplier, registry); err == nil {
		t.Fatal("expected incompatible scalar addition to be rejected")
	}
}

func typedRuleFixture(t *testing.T) (materialization.RuleSetV1, *formula.Registry, domain.ID, domain.ID) {
	t.Helper()
	registry, err := formula.V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	project, revision, run, attribute, effect, skill := engineRuleID(t), engineRuleID(t), engineRuleID(t), engineRuleID(t), engineRuleID(t), engineRuleID(t)
	parsed := formula.Parse("1", registry)
	ast, _ := parsed.AST.CanonicalBytes()
	hash, _ := parsed.AST.Hash()
	entities := []domain.Entity{{ID: attribute, Kind: domain.KindAttribute, Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{}, Extensions: map[string]json.RawMessage{}}, {ID: effect, Kind: domain.KindEffect, Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"modifiers": json.RawMessage(`[{"attribute_id":"` + string(attribute) + `","operation":"Add","value":"1"}]`), "stack_rule": json.RawMessage(`{"operation":"Add","priority":"1","max_stacks":"2","refresh_policy":"refresh","cap":"10"}`)}, Extensions: map[string]json.RawMessage{}}, {ID: skill, Kind: domain.KindSkill, Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"costs": json.RawMessage(`[{"output_attribute_id":"` + string(attribute) + `","expression":"1"}]`), "rule_blocks": json.RawMessage(`[{"event":"on_use","target":{"type":"self"},"effect_ids":["` + string(effect) + `"]}]`)}, Extensions: map[string]json.RawMessage{}}}
	rules, err := materialization.Build(materialization.Source{ProjectID: project, RevisionID: revision, ConfigHash: strings.Repeat("a", 64), Certification: materialization.Certification{RunID: run, ResultHash: strings.Repeat("b", 64), Versions: validation.VersionManifest{Schema: "schema-v1", DSL: formula.DSLVersion, Registry: "registry-v1", NumericPolicy: "numeric-v1"}}, Entities: entities, Formulas: []validation.FormulaIndexRecord{{SourceID: skill, FieldPath: "/payload/costs/0/expression", OutputAttributeID: attribute, AST: ast, ASTHash: hash, ASTVersion: formula.ASTSchemaVersion, DSLVersion: formula.DSLVersion, RegistryVersion: "registry-v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	return rules, registry, attribute, effect
}

func engineRuleID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func engineDecimal(t *testing.T, text string) formula.Decimal {
	t.Helper()
	value, err := formula.ParseDecimal(text)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
