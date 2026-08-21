package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

func TestScenarioDefinitionsSeedExactlyFourImmutableBuiltins(t *testing.T) {
	store := newStore(t)
	definitions, err := store.ListScenarioDefinitions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 4 {
		t.Fatalf("definitions=%d", len(definitions))
	}
	definition, err := store.GetScenarioDefinition(context.Background(), "single-target-30s", "v1")
	if err != nil || definition.Template.Origin != "builtin" || definition.Template.BodyHash == "" {
		t.Fatalf("definition=%#v err=%v", definition, err)
	}
	if _, err := store.db.Exec(`UPDATE scenario_definitions SET origin='clone' WHERE id=?`, definition.ID); err == nil {
		t.Fatal("expected immutable scenario definition trigger")
	}
	if _, err := store.GetScenarioDefinition(context.Background(), "missing", "v1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing definition err=%v", err)
	}
}

func TestScenarioDefinitionClonePersistsNewImmutableVersion(t *testing.T) {
	store := newStore(t)
	clone, err := store.CloneScenarioDefinition(context.Background(), "single-target-30s", "v1", "single-target-custom", "v2", []scenario.ParameterOverlay{{Path: "/actions/opening-strike/inputs/amount", Value: json.RawMessage(`"20"`)}}, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if clone.Template.Origin != "clone" || clone.Template.Definition.Actions[0].Inputs[0].Value != "20" {
		t.Fatalf("clone=%#v", clone)
	}
	if !clone.SourceDefinitionID.Valid() {
		t.Fatalf("clone lineage missing: %#v", clone)
	}
	if _, err = store.CloneScenarioDefinition(context.Background(), "single-target-30s", "v1", "single-target-custom", "v2", nil, func(string) bool { return true }); err == nil {
		t.Fatal("expected scene/version uniqueness violation")
	}
	builtin, err := store.GetScenarioDefinition(context.Background(), "single-target-30s", "v1")
	if err != nil || builtin.Template.Definition.Actions[0].Inputs[0].Value != "10" {
		t.Fatalf("builtin=%#v err=%v", builtin, err)
	}
	if clone.SourceDefinitionID != builtin.ID {
		t.Fatalf("clone source=%s builtin=%s", clone.SourceDefinitionID, builtin.ID)
	}
}
