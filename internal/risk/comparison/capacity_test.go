package comparison

import (
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

func TestComparisonCapacityAtFiveHundredPolicyMetrics(t *testing.T) {
	requests := make([]Request, 500)
	for index := range requests {
		metricID := fmt.Sprintf("metric-resource-%03d", index)
		candidate := metric(metricID, "ratio", riskcontract.HigherIsRisk, "125")
		baseline := metric(metricID, "ratio", riskcontract.HigherIsRisk, "100")
		requests[index] = compareRequest(fmt.Sprintf("capacity-%03d", index), riskcontract.Required, candidate, baseline, relativeEntry(metricID, "ratio", riskcontract.HigherIsRisk))
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	started := time.Now()
	results := CompareAll(requests)
	elapsed := time.Since(started)
	runtime.ReadMemStats(&after)
	payload, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range results {
		if result.ID != fmt.Sprintf("capacity-%03d", index) || result.Status != riskcontract.Comparable || result.Severity == nil || *result.Severity != riskcontract.Block {
			t.Fatalf("result[%d]=%#v", index, result)
		}
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if len(results) != 500 || len(payload) > 8<<20 || allocated > 128<<20 || elapsed > 2*time.Second {
		t.Fatalf("results=%d payload=%d allocated=%d elapsed=%s", len(results), len(payload), allocated, elapsed)
	}
	t.Logf("risk-comparison metrics=500 elapsed=%s allocated_bytes=%d payload_bytes=%d", elapsed, allocated, len(payload))
}
