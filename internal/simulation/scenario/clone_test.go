package scenario

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCloneCreatesNewVersionAndAppliesOnlyDeclaredOverlay(t *testing.T) {
	template := BuiltinTemplates()[0]
	clone, err := Clone(template, "custom-single", "v2", []ParameterOverlay{{Path: "/actions/opening-strike/inputs/amount", Value: json.RawMessage(`"12.5"`)}}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if clone.Origin != "clone" || clone.Definition.ID != "custom-single" || clone.Definition.Version != "v2" || clone.BodyHash == template.BodyHash || clone.Definition.Actions[0].Inputs[0].Value != "12.5" {
		t.Fatalf("clone=%#v", clone)
	}
	if template.Definition.ID != "single-target-30s" || template.Definition.Actions[0].Inputs[0].Value != "10" {
		t.Fatal("clone mutated built-in template")
	}
}

func TestCloneRejectsUndeclaredInvalidAndUnavailableValues(t *testing.T) {
	template := BuiltinTemplates()[0]
	for name, testCase := range map[string]struct {
		overlays  []ParameterOverlay
		available bool
	}{
		"undeclared":              {overlays: []ParameterOverlay{{Path: "/script", Value: json.RawMessage(`"run"`)}}, available: true},
		"out of range":            {overlays: []ParameterOverlay{{Path: "/actions/opening-strike/inputs/amount", Value: json.RawMessage(`"1001"`)}}, available: true},
		"unavailable participant": {available: false},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Clone(template, "custom", "v1", testCase.overlays, func(string) bool { return testCase.available })
			if err == nil || !strings.Contains(err.Error(), "scenario") && !strings.Contains(err.Error(), "parameter") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestCloneRejectsDeclaredParameterWithWrongUnit(t *testing.T) {
	template := BuiltinTemplates()[0]
	definition := template.Definition
	definition.Parameters[0].Unit = "milliseconds"
	body, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	template = templateFromCanonical(definition, "builtin", body)
	_, err = Clone(template, "custom", "v1", []ParameterOverlay{{Path: "/actions/opening-strike/inputs/amount", Value: json.RawMessage(`"10"`)}}, func(string) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "unit") {
		t.Fatalf("err=%v", err)
	}
}

func TestCloneCanonicalHashDoesNotDependOnOverlayOrder(t *testing.T) {
	template := twoParameterTemplate(t)
	first, err := Clone(template, "custom", "v1", []ParameterOverlay{{Path: "/actions/one/inputs/amount", Value: json.RawMessage(`"20"`)}, {Path: "/actions/two/inputs/amount", Value: json.RawMessage(`"30"`)}}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	second, err := Clone(template, "custom", "v1", []ParameterOverlay{{Path: "/actions/two/inputs/amount", Value: json.RawMessage(`"30"`)}, {Path: "/actions/one/inputs/amount", Value: json.RawMessage(`"20"`)}}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if first.BodyHash != second.BodyHash || string(first.Body) != string(second.Body) {
		t.Fatalf("hashes %s != %s", first.BodyHash, second.BodyHash)
	}
	if _, err = Clone(template, "custom", "v1", []ParameterOverlay{{Path: "/actions/one/inputs/amount", Value: json.RawMessage(`"20"`)}, {Path: "/actions/one/inputs/amount", Value: json.RawMessage(`"21"`)}}, func(string) bool { return true }); err == nil {
		t.Fatal("expected duplicate overlay rejection")
	}
}

func twoParameterTemplate(t *testing.T) Template {
	t.Helper()
	definition := Definition{ID: "source", Version: "v1", Participants: []Participant{{ID: "source", Kind: "actor"}, {ID: "target", Kind: "target"}}, Actions: []Action{{ID: "one", EventID: "damage-v1", EvaluatorID: "combat-v1", SourceID: "source", TargetID: "target", Inputs: []Attribute{{ID: "amount", Value: "10", Unit: "points"}}}, {ID: "two", EventID: "damage-v1", EvaluatorID: "combat-v1", AtMS: 1, SourceID: "source", TargetID: "target", Inputs: []Attribute{{ID: "amount", Value: "10", Unit: "points"}}}}, DurationMS: 10, Parameters: []Parameter{{Path: "/actions/one/inputs/amount", Type: ParameterDecimal, DefaultValue: json.RawMessage(`"10"`), Minimum: "0", Maximum: "100", Unit: "points"}, {Path: "/actions/two/inputs/amount", Type: ParameterDecimal, DefaultValue: json.RawMessage(`"10"`), Minimum: "0", Maximum: "100", Unit: "points"}}, Budgets: Budgets{MaxEvents: 10, MaxSteps: 10, MaxSamples: 10, MaxRuntimeMS: 10}}
	body, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	return templateFromCanonical(definition, "builtin", body)
}
