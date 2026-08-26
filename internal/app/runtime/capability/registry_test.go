package capability

import (
	"errors"
	"reflect"
	"testing"
)

func descriptor(id, version string, prerequisites ...Prerequisite) Descriptor {
	return Descriptor{ID: id, Version: version, Prerequisites: prerequisites, Evaluator: func(EvaluationContext) Result { return AvailableResult() }}
}

func TestRegistryRejectsDuplicateConflictingVersionAndCycle(t *testing.T) {
	if _, err := NewRegistry(descriptor("editing", "1"), descriptor("editing", "1")); !errors.Is(err, ErrDescriptorDuplicate) {
		t.Fatalf("duplicate err=%v", err)
	}
	if _, err := NewRegistry(descriptor("editing", "1"), descriptor("editing", "2")); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version err=%v", err)
	}
	if _, err := NewRegistry(
		descriptor("one", "1", Prerequisite{CapabilityID: "two", Required: true}),
		descriptor("two", "1", Prerequisite{CapabilityID: "one", Required: true}),
	); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("cycle err=%v", err)
	}
}

func TestRegistryEvaluatesRequiredOptionalAndUnregisteredPrerequisites(t *testing.T) {
	registry, err := NewRegistry(
		Descriptor{ID: "graph", Version: "1", Evaluator: func(EvaluationContext) Result {
			return Result{State: Unavailable, ObservationGeneration: 7, Reasons: []Reason{{Code: "GRAPH_DOWN", Component: "graph", ObservationGeneration: 7}}}
		}},
		descriptor("impact", "1", Prerequisite{CapabilityID: "graph", Required: true}),
		descriptor("editing", "1", Prerequisite{CapabilityID: "optional-module", Required: false}),
	)
	if err != nil {
		t.Fatal(err)
	}
	results := registry.Evaluate(nil)
	wantStates := map[string]State{"editing": Degraded, "graph": Unavailable, "impact": Unavailable}
	for _, result := range results {
		if result.State != wantStates[result.ID] {
			t.Fatalf("result=%#v", result)
		}
		if result.ID == "impact" && (result.ObservationGeneration != 7 || result.Reasons[0].Code != "REQUIRED_CAPABILITY_UNAVAILABLE") {
			t.Fatalf("impact=%#v", result)
		}
	}
}

func TestRegistryEvaluationIsDeterministicAndDetached(t *testing.T) {
	registry, err := NewRegistry(Descriptor{ID: "zeta", Version: "1.0", Evaluator: func(context EvaluationContext) Result {
		context.Observations["input"] = Observation{ID: "mutated"}
		return Result{State: Degraded, Reasons: []Reason{{Code: "Z_REASON", Component: "zeta"}, {Code: "A_REASON", Component: "zeta"}}, Actions: []Action{{ID: "z"}, {ID: "a"}}}
	}}, descriptor("alpha", "1"))
	if err != nil {
		t.Fatal(err)
	}
	observations := map[string]Observation{"input": {ID: "input", Reasons: []Reason{{Code: "ORIGINAL", Component: "input"}}}}
	first, second := registry.Evaluate(observations), registry.Evaluate(observations)
	if !reflect.DeepEqual(first, second) || first[0].ID != "alpha" || first[1].Reasons[0].Code != "A_REASON" || first[1].Actions[0].ID != "a" {
		t.Fatalf("results=%#v", first)
	}
	if observations["input"].ID != "input" {
		t.Fatal("evaluator mutated caller observation")
	}
	first[1].Reasons[0].Code = "MUTATED"
	if registry.Evaluate(observations)[1].Reasons[0].Code != "A_REASON" {
		t.Fatal("result was not detached")
	}
}
