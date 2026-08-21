package contract

import (
	"errors"
	"testing"

	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

func TestNormalizeInputExpandsDefaultsAndOrdersMetrics(t *testing.T) {
	template := scenario.BuiltinTemplates()[0]
	revision := Revision{ID: "revision", ProjectID: "project", ConfigHash: "config", ManifestHash: "manifest"}
	input, err := NormalizeInput(InputRequest{Revision: revision, Scene: template, Metrics: []MetricIdentity{{ID: "metric-resource", Version: "v1"}, {ID: "metric-dps", Version: "v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if input.SampleCount != DefaultSampleCount || input.Seed != template.Definition.DefaultSeed || input.Metrics[0].ID != "metric-dps" || len(input.Actions) != len(template.Definition.Actions) {
		t.Fatalf("input=%#v", input)
	}
}

func TestNormalizeInputRejectsNoncanonicalSceneAndBudgetViolations(t *testing.T) {
	template := scenario.BuiltinTemplates()[0]
	revision := Revision{ID: "revision", ProjectID: "project", ConfigHash: "config", ManifestHash: "manifest"}
	template.Body = append(template.Body, ' ')
	if _, err := NormalizeInput(InputRequest{Revision: revision, Scene: template, Metrics: []MetricIdentity{{ID: "metric-dps", Version: "v1"}}}); !errors.Is(err, ErrInputInvalid) {
		t.Fatalf("noncanonical err=%v", err)
	}
	template = scenario.BuiltinTemplates()[0]
	if _, err := NormalizeInput(InputRequest{Revision: revision, Scene: template, SampleCount: template.Definition.Budgets.MaxSamples + 1, Metrics: []MetricIdentity{{ID: "metric-dps", Version: "v1"}}}); !errors.Is(err, ErrInputInvalid) {
		t.Fatalf("budget err=%v", err)
	}
	if _, err := NormalizeInput(InputRequest{Revision: revision, Scene: template, Metrics: []MetricIdentity{{ID: "metric-dps", Version: "v1"}, {ID: "metric-dps", Version: "v1"}}}); !errors.Is(err, ErrInputInvalid) {
		t.Fatalf("metrics err=%v", err)
	}
}

func TestNormalizedInputHashIsStableForEquivalentDefaultsAndChangesWithSeed(t *testing.T) {
	template := scenario.BuiltinTemplates()[0]
	revision := Revision{ID: "revision", ProjectID: "project", ConfigHash: "config", ManifestHash: "manifest"}
	base := InputRequest{Revision: revision, Scene: template, Metrics: []MetricIdentity{{ID: "metric-dps", Version: "v1"}}}
	left, err := NormalizeInput(base)
	if err != nil {
		t.Fatal(err)
	}
	count, seed := DefaultSampleCount, template.Definition.DefaultSeed
	right, err := NormalizeInput(InputRequest{Revision: revision, Scene: template, SampleCount: count, Seed: &seed, Metrics: []MetricIdentity{{ID: "metric-dps", Version: "v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	leftHash, err := left.Hash()
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := right.Hash()
	if err != nil || leftHash != rightHash {
		t.Fatalf("hashes %s %s err=%v", leftHash, rightHash, err)
	}
	changedSeed := seed + 1
	changed, err := NormalizeInput(InputRequest{Revision: revision, Scene: template, Seed: &changedSeed, Metrics: base.Metrics})
	if err != nil {
		t.Fatal(err)
	}
	changedHash, _ := changed.Hash()
	if changedHash == leftHash {
		t.Fatal("seed did not change input identity")
	}
}

func TestInputHashChangesForEveryCapturedSemanticFact(t *testing.T) {
	template := scenario.BuiltinTemplates()[0]
	base, err := NormalizeInput(InputRequest{Revision: Revision{ID: "revision", ProjectID: "project", ConfigHash: "config", ManifestHash: "manifest"}, Scene: template, Metrics: []MetricIdentity{{ID: "metric-dps", Version: "v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	baseHash, err := base.Hash()
	if err != nil {
		t.Fatal(err)
	}
	changes := []struct {
		name   string
		mutate func(*SimulationInputV1)
	}{
		{"revision", func(input *SimulationInputV1) { input.RevisionID = "other-revision" }},
		{"config", func(input *SimulationInputV1) { input.ConfigHash = "other-config" }},
		{"scene", func(input *SimulationInputV1) { input.SceneBodyHash = "other-scene" }},
		{"participant", func(input *SimulationInputV1) { input.Participants[0].ID = "other-source" }},
		{"action", func(input *SimulationInputV1) { input.Actions[0].AtMS = 1 }},
		{"budget", func(input *SimulationInputV1) { input.Budgets.MaxEvents++ }},
		{"sample", func(input *SimulationInputV1) { input.SampleCount-- }},
		{"seed", func(input *SimulationInputV1) { input.Seed++ }},
		{"metric", func(input *SimulationInputV1) { input.Metrics[0].Version = "v2" }},
	}
	for _, change := range changes {
		t.Run(change.name, func(t *testing.T) {
			candidate := cloneInput(base)
			change.mutate(&candidate)
			hash, hashErr := candidate.Hash()
			if hashErr != nil || hash == baseHash {
				t.Fatalf("hash=%s base=%s err=%v", hash, baseHash, hashErr)
			}
		})
	}
}

func cloneInput(input SimulationInputV1) SimulationInputV1 {
	copy := input
	copy.Participants = append([]scenario.Participant(nil), input.Participants...)
	copy.Actions = append([]scenario.Action(nil), input.Actions...)
	copy.Metrics = append([]MetricIdentity(nil), input.Metrics...)
	return copy
}
