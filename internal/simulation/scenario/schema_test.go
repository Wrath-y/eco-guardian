package scenario

import "testing"

func TestDefinitionRejectsUnknownOrExecutableFields(t *testing.T) {
	body := []byte(`{"id":"single-target","version":"v1","participants":[{"id":"source","kind":"actor","attributes":[]},{"id":"target","kind":"actor","attributes":[]}],"actions":[{"id":"strike","event_id":"damage","evaluator_id":"combat-v1","at_ms":0,"source_id":"source","target_id":"target"}],"duration_ms":30000,"default_seed":1,"parameters":[],"budgets":{"max_events":100,"max_steps":100,"max_samples":1000,"max_runtime_ms":1000},"script":"return exploit()"}`)
	if _, err := ParseDefinition(body); err == nil {
		t.Fatal("expected executable field to be rejected")
	}
}

func TestDefinitionRejectsUnknownParticipantsAndInvalidBudgets(t *testing.T) {
	definition := Definition{ID: "single-target", Version: "v1", Participants: []Participant{{ID: "source", Kind: "actor"}}, Actions: []Action{{ID: "strike", EventID: "damage", EvaluatorID: "combat-v1", SourceID: "source", TargetID: "target"}}, DurationMS: 30_000, Budgets: Budgets{MaxEvents: 1, MaxSteps: 1, MaxSamples: 1, MaxRuntimeMS: 1}}
	if err := definition.Validate(); err == nil {
		t.Fatal("expected unknown target rejection")
	}
	definition.Actions = nil
	definition.Budgets.MaxEvents = 0
	if err := definition.Validate(); err == nil {
		t.Fatal("expected invalid budget rejection")
	}
}
