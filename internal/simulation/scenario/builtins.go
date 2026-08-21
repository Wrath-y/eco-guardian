package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// BuiltinTemplates is the closed v1 starter set. Its canonical JSON derives
// only from typed definitions, so hashes are stable across process launches.
func BuiltinTemplates() []Template {
	return []Template{
		builtinTemplate("single-target-30s", 30_000, 11, 1, false),
		builtinTemplate("single-target-180s", 180_000, 12, 1, false),
		builtinTemplate("three-target-60s", 60_000, 13, 3, false),
		builtinTemplate("extreme-stacking-60s", 60_000, 14, 1, true),
	}
}

func NewBuiltinRegistry() (*Registry, error) {
	return NewRegistry(BuiltinTemplates(), []string{"damage-v1"}, []string{"combat-v1"})
}

func builtinTemplate(id string, duration int64, seed uint64, targets int, stacked bool) Template {
	participants := []Participant{{ID: "source", Kind: "actor", Attributes: []Attribute{{ID: "power", Value: "100", Unit: "points"}}}}
	for index := 1; index <= targets; index++ {
		participants = append(participants, Participant{ID: "target-" + string(rune('0'+index)), Kind: "target", Attributes: []Attribute{{ID: "health", Value: "1000", Unit: "points"}}})
	}
	actions := []Action{{ID: "opening-strike", EventID: "damage-v1", EvaluatorID: "combat-v1", AtMS: 0, SourceID: "source", TargetID: "target-1", Inputs: []Attribute{{ID: "amount", Value: "10", Unit: "points"}}}}
	if targets == 3 {
		actions = append(actions, Action{ID: "second-strike", EventID: "damage-v1", EvaluatorID: "combat-v1", AtMS: 1_000, SourceID: "source", TargetID: "target-2", Inputs: []Attribute{{ID: "amount", Value: "10", Unit: "points"}}}, Action{ID: "third-strike", EventID: "damage-v1", EvaluatorID: "combat-v1", AtMS: 2_000, SourceID: "source", TargetID: "target-3", Inputs: []Attribute{{ID: "amount", Value: "10", Unit: "points"}}})
	}
	if stacked {
		actions = append(actions, Action{ID: "stacked-strike", EventID: "damage-v1", EvaluatorID: "combat-v1", AtMS: 1, SourceID: "source", TargetID: "target-1", Inputs: []Attribute{{ID: "amount", Value: "999", Unit: "points"}}})
	}
	definition := Definition{ID: id, Version: "v1", Participants: participants, Actions: actions, DurationMS: duration, DefaultSeed: seed, Parameters: []Parameter{}, Budgets: Budgets{MaxEvents: 10_000, MaxSteps: 10_000, MaxSamples: 1_000, MaxRuntimeMS: 30_000}}
	body, err := json.Marshal(definition)
	if err != nil {
		panic(err)
	}
	hash := sha256.Sum256(body)
	return Template{Definition: definition, Origin: "builtin", Body: body, BodyHash: hex.EncodeToString(hash[:])}
}
