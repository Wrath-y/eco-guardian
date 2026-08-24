// Package fixture provides versioned, fixed simulation inputs shared by
// determinism, recovery and end-to-end suites. It is deliberately data-only:
// callers still use the normal registry, admission and engine paths.
package fixture

import (
	"encoding/json"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/engine"
	"github.com/zouyi/eco-guardian/internal/simulation/metric"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

const V1 = "simulation-fixture-v1"

// V1Fixture is a complete fixed input catalogue. The bounded trigger and
// stack values are canonical #6 representations, never executable source.
type V1Fixture struct {
	Version             string
	Entities            []domain.Entity
	Templates           []scenario.Template
	Metrics             []metric.Descriptor
	SimultaneousEvents  []engine.Event
	RandomSeed          uint64
	RandomSampleOrdinal uint64
	DecimalBoundary     string
	DecimalUnit         string
	UnavailableMissing  []string
	TriggerEvent        string
	TriggerBudgetMS     string
	StackMax            string
	StackCap            string
}

// FixedV1 returns a fresh copy of the fixture. Stable UUIDv7 identities are
// intentionally literal so a fixture can be reproduced across processes.
func FixedV1() V1Fixture {
	metrics := make([]metric.Descriptor, 0, len(metric.V1Modules()))
	for _, module := range metric.V1Modules() {
		metrics = append(metrics, module.Descriptor())
	}
	return V1Fixture{
		Version: V1,
		Entities: []domain.Entity{
			fixtureEntity("01948c1e-0000-7000-8000-000000000010", domain.KindAttribute),
			fixtureEntity("01948c1e-0000-7000-8000-000000000011", domain.KindTag),
			fixtureEntity("01948c1e-0000-7000-8000-000000000012", domain.KindCharacter),
			fixtureEntity("01948c1e-0000-7000-8000-000000000013", domain.KindSkill),
			fixtureEntity("01948c1e-0000-7000-8000-000000000014", domain.KindItem),
			fixtureEntity("01948c1e-0000-7000-8000-000000000015", domain.KindEffect),
		},
		Templates: scenario.BuiltinTemplates(),
		Metrics:   metrics,
		SimultaneousEvents: []engine.Event{
			{TimeMS: 100, RulePriority: 1, SourceID: "fixture-source-a", InsertionOrdinal: 0, Kind: "damage-v1", Version: "v1", EvaluatorID: "combat-v1", TargetID: "fixture-target"},
			{TimeMS: 100, RulePriority: 1, SourceID: "fixture-source-b", InsertionOrdinal: 1, Kind: "damage-v1", Version: "v1", EvaluatorID: "combat-v1", TargetID: "fixture-target"},
		},
		RandomSeed:          11,
		RandomSampleOrdinal: 7,
		DecimalBoundary:     "1e6144",
		DecimalUnit:         "damage_point",
		UnavailableMissing:  []string{"healing_per_second"},
		TriggerEvent:        "on_damage",
		TriggerBudgetMS:     "1",
		StackMax:            "2",
		StackCap:            "10",
	}
}

func fixtureEntity(id string, kind domain.EntityKind) domain.Entity {
	return domain.Entity{ID: domain.ID(id), Kind: kind, Key: string(kind) + "-fixture", Name: string(kind) + " fixture", Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{}, Extensions: map[string]json.RawMessage{}}
}
