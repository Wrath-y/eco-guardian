package metric

import (
	"testing"

	"github.com/zouyi/eco-guardian/internal/formula"
)

type fakeModule struct {
	descriptor Descriptor
	result     Result
}

func (module fakeModule) Descriptor() Descriptor          { return module.descriptor }
func (module fakeModule) Evaluate(Sample) (Result, error) { return module.result, nil }

func metricDescriptor(id string) Descriptor {
	return Descriptor{ID: id, Version: "v1", RequiredObservations: []string{"damage"}, Unit: "points_per_second", Direction: HigherIsRisk, AggregationVersion: "v1", ConfidenceVersion: "v1", Assumptions: []string{"independent_samples"}}
}
func TestMetricRegistryOrdersDescriptorsAndRejectsDuplicates(t *testing.T) {
	left, right := metricDescriptor("metric-dps"), metricDescriptor("metric-healing")
	registry, err := NewRegistry([]Module{fakeModule{descriptor: right}, fakeModule{descriptor: left}})
	if err != nil {
		t.Fatal(err)
	}
	descriptors := registry.Descriptors()
	if len(descriptors) != 2 || descriptors[0].ID != "metric-dps" {
		t.Fatalf("descriptors=%#v", descriptors)
	}
	if _, err = NewRegistry([]Module{fakeModule{descriptor: left}, fakeModule{descriptor: left}}); err == nil {
		t.Fatal("expected duplicate registry error")
	}
}
func TestUnavailableMetricNeverCarriesZeroValue(t *testing.T) {
	descriptor := metricDescriptor("metric-healing")
	result := MissingResult(descriptor, []string{"healing_events", "healing_events"}, "MISSING_STRUCTURED_INPUT", "Healing events are unavailable")
	if !result.Valid() || result.Value != nil || result.Unavailable.Missing[0] != "healing_events" {
		t.Fatalf("result=%#v", result)
	}
	zero, err := formula.ParseDecimal("0")
	if err != nil {
		t.Fatal(err)
	}
	result.Value = &zero
	if result.Valid() {
		t.Fatal("unavailable metric accepted a zero substitute")
	}
}

func TestV1ModulesAreIndependentAndUseExactDecimalObservations(t *testing.T) {
	registry, err := V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	value, err := formula.ParseDecimal("12.5")
	if err != nil {
		t.Fatal(err)
	}
	dps, found := registry.Module("metric-dps")
	if !found {
		t.Fatal("DPS module missing")
	}
	result, err := dps.Evaluate(Sample{Ordinal: 3, Observations: []Observation{{ID: "damage_per_second", Unit: "points_per_second", Value: value}}})
	if err != nil || !result.Valid() || result.Value.String() != "12.5" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	healing, _ := registry.Module("metric-healing")
	unavailable, err := healing.Evaluate(Sample{Ordinal: 3, Observations: []Observation{{ID: "damage_per_second", Unit: "points_per_second", Value: value}}})
	if err != nil || unavailable.Status != Unavailable || unavailable.Value != nil {
		t.Fatalf("unavailable=%#v err=%v", unavailable, err)
	}
}
