package metric

import (
	"fmt"

	"github.com/zouyi/eco-guardian/internal/formula"
)

type observationModule struct {
	descriptor    Descriptor
	observationID string
}

func (module observationModule) Descriptor() Descriptor { return module.descriptor }
func (module observationModule) Evaluate(sample Sample) (Result, error) {
	var total *formula.Decimal
	for _, observation := range sample.Observations {
		if observation.ID != module.observationID {
			continue
		}
		if observation.Unit != module.descriptor.Unit {
			return Result{}, fmt.Errorf("metric %s received incompatible unit %q", module.descriptor.ID, observation.Unit)
		}
		if total == nil {
			value := observation.Value
			total = &value
			continue
		}
		value, err := formula.Add(*total, observation.Value)
		if err != nil {
			return Result{}, err
		}
		total = &value
	}
	if total == nil {
		return MissingResult(module.descriptor, []string{module.observationID}, "MISSING_STRUCTURED_INPUT", "Required structured observation is unavailable"), nil
	}
	return Result{Descriptor: module.descriptor, Status: Available, Value: total}, nil
}

func V1Modules() []Module {
	resourceLower, err := formula.ParseDecimal("0.8")
	if err != nil {
		panic("invalid metric-resource v1 lower target bound")
	}
	resourceUpper, err := formula.ParseDecimal("1.2")
	if err != nil {
		panic("invalid metric-resource v1 upper target bound")
	}
	return []Module{
		observationModule{descriptor: Descriptor{ID: "metric-dps", Version: "v1", RequiredObservations: []string{"damage_per_second"}, Unit: "points_per_second", Direction: HigherIsRisk, AggregationVersion: AggregationV1, ConfidenceVersion: ConfidenceV1, Assumptions: []string{"independent_samples", "structured_damage_events"}}, observationID: "damage_per_second"},
		observationModule{descriptor: Descriptor{ID: "metric-healing", Version: "v1", RequiredObservations: []string{"healing_per_second"}, Unit: "points_per_second", Direction: HigherIsRisk, AggregationVersion: AggregationV1, ConfidenceVersion: ConfidenceV1, Assumptions: []string{"independent_samples", "structured_healing_events"}}, observationID: "healing_per_second"},
		observationModule{descriptor: Descriptor{ID: "metric-survivability", Version: "v1", RequiredObservations: []string{"survival_seconds"}, Unit: "milliseconds", Direction: LowerIsRisk, AggregationVersion: AggregationV1, ConfidenceVersion: ConfidenceV1, Assumptions: []string{"independent_samples", "terminal_state_observation"}}, observationID: "survival_seconds"},
		observationModule{descriptor: Descriptor{ID: "metric-resource", Version: "v1", RequiredObservations: []string{"resource_efficiency"}, Unit: "ratio", Direction: TargetRange, TargetRange: &ValueRange{Lower: resourceLower, Upper: resourceUpper, Bounds: Inclusive}, AggregationVersion: AggregationV1, ConfidenceVersion: ConfidenceV1, Assumptions: []string{"independent_samples", "resource_state_observation"}}, observationID: "resource_efficiency"},
		observationModule{descriptor: Descriptor{ID: "metric-control", Version: "v1", RequiredObservations: []string{"control_duration"}, Unit: "milliseconds", Direction: HigherIsRisk, AggregationVersion: AggregationV1, ConfidenceVersion: ConfidenceV1, Assumptions: []string{"independent_samples", "structured_control_events"}}, observationID: "control_duration"},
	}
}

func V1Registry() (*Registry, error) { return NewRegistry(V1Modules()) }
