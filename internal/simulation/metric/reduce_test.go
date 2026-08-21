package metric

import (
	"math/rand/v2"
	"testing"

	"github.com/zouyi/eco-guardian/internal/formula"
)

func observation(id, value string) Observation {
	decimal, _ := formula.ParseDecimal(value)
	return Observation{ID: id, Value: decimal, Unit: "points_per_second"}
}
func TestReducerSortsSamplesAndMetricsWithoutCompletionOrder(t *testing.T) {
	registry, err := NewRegistry([]Module{observationModule{descriptor: metricDescriptor("metric-z"), observationID: "damage"}, observationModule{descriptor: metricDescriptor("metric-a"), observationID: "damage"}})
	if err != nil {
		t.Fatal(err)
	}
	left, err := Reduce(registry, []Sample{{Ordinal: 2, Observations: []Observation{observation("damage", "3")}}, {Ordinal: 1, Observations: []Observation{observation("damage", "1")}}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := Reduce(registry, []Sample{{Ordinal: 1, Observations: []Observation{observation("damage", "1")}}, {Ordinal: 2, Observations: []Observation{observation("damage", "3")}}})
	if err != nil || left[0].Descriptor.ID != "metric-a" || left[0].Value.String() != "2" || right[0].Value.String() != left[0].Value.String() {
		t.Fatalf("left=%#v right=%#v err=%v", left, right, err)
	}
}

func TestReducerPreservesUnavailableWithoutChangingAvailableMetric(t *testing.T) {
	available := observationModule{descriptor: metricDescriptor("metric-dps"), observationID: "damage"}
	unavailable := observationModule{descriptor: Descriptor{ID: "metric-healing", Version: "v1", RequiredObservations: []string{"healing"}, Unit: "points_per_second", Direction: HigherIsRisk, AggregationVersion: AggregationV1, ConfidenceVersion: ConfidenceV1}, observationID: "healing"}
	registry, err := NewRegistry([]Module{available, unavailable})
	if err != nil {
		t.Fatal(err)
	}
	results, err := Reduce(registry, []Sample{{Ordinal: 0, Observations: []Observation{observation("damage", "4")}}})
	if err != nil || results[0].Status != Available || results[1].Status != Unavailable || results[1].Value != nil {
		t.Fatalf("results=%#v err=%v", results, err)
	}
}

func TestReducerIsStableAcrossRepeatedShuffledCompletionOrders(t *testing.T) {
	registry, err := NewRegistry([]Module{observationModule{descriptor: metricDescriptor("metric-dps"), observationID: "damage"}})
	if err != nil {
		t.Fatal(err)
	}
	base := []Sample{}
	for ordinal := uint64(0); ordinal < 20; ordinal++ {
		base = append(base, Sample{Ordinal: ordinal, Observations: []Observation{observation("damage", "1")}})
	}
	first, err := Reduce(registry, base)
	if err != nil {
		t.Fatal(err)
	}
	for seed := uint64(1); seed <= 20; seed++ {
		shuffled := append([]Sample(nil), base...)
		random := rand.New(rand.NewPCG(seed, seed+1))
		random.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		result, reduceErr := Reduce(registry, shuffled)
		if reduceErr != nil || result[0].Value.String() != first[0].Value.String() || result[0].ConfidenceLow.String() != first[0].ConfidenceLow.String() || result[0].ConfidenceHigh.String() != first[0].ConfidenceHigh.String() {
			t.Fatalf("seed=%d result=%#v err=%v", seed, result, reduceErr)
		}
	}
}
