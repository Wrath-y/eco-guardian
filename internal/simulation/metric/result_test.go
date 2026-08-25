package metric

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/formula"
)

func TestResultHashExcludesSchedulingAndCanonicalizesMetricWarnings(t *testing.T) {
	value, err := formula.ParseDecimal("2")
	if err != nil {
		t.Fatal(err)
	}
	aggregate := AggregateResult{Descriptor: metricDescriptor("metric-dps"), Status: Available, Value: &value, ConfidenceLow: &value, ConfidenceHigh: &value, SampleCount: 2}
	inputHash, fingerprintHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	left, err := NewCanonicalResult(inputHash, fingerprintHash, []AggregateResult{aggregate}, []string{"z-warning", "a-warning"})
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewCanonicalResult(inputHash, fingerprintHash, []AggregateResult{aggregate}, []string{"a-warning", "z-warning"})
	if err != nil {
		t.Fatal(err)
	}
	leftHash, _ := left.Hash()
	rightHash, _ := right.Hash()
	if leftHash != rightHash || left.Warnings[0] != "a-warning" {
		t.Fatalf("hashes %s %s warnings=%v", leftHash, rightHash, left.Warnings)
	}
	changed := aggregate
	changed.SampleCount = 3
	different, err := NewCanonicalResult(inputHash, fingerprintHash, []AggregateResult{changed}, []string{"a-warning", "z-warning"})
	if err != nil {
		t.Fatal(err)
	}
	differentHash, _ := different.Hash()
	if differentHash == leftHash {
		t.Fatal("semantic result change did not change hash")
	}
}

func TestCanonicalResultSealsTargetRangeAndOptionalAbsoluteThreshold(t *testing.T) {
	value, _ := formula.ParseDecimal("1")
	lower, _ := formula.ParseDecimal("0.8")
	upper, _ := formula.ParseDecimal("1.2")
	descriptor := metricDescriptor("metric-resource")
	descriptor.Direction = TargetRange
	descriptor.Unit = "ratio"
	descriptor.TargetRange = &ValueRange{Lower: lower, Upper: upper, Bounds: Inclusive}
	aggregate := AggregateResult{Descriptor: descriptor, Status: Available, Value: &value, ConfidenceLow: &value, ConfidenceHigh: &value, SampleCount: 1}
	result, err := NewCanonicalResult(strings.Repeat("a", 64), strings.Repeat("b", 64), []AggregateResult{aggregate}, nil)
	if err != nil {
		t.Fatal(err)
	}
	metric := result.Metrics[0]
	if metric.TargetRange == nil || metric.TargetRange.Lower != "0.8" || metric.TargetRange.Upper != "1.2" || metric.TargetRange.Bounds != Inclusive || metric.AbsoluteThreshold != nil {
		t.Fatalf("canonical metric=%#v", metric)
	}
	body, err := json.Marshal(metric)
	if err != nil || !strings.Contains(string(body), `"target_range":{"lower":"0.8","upper":"1.2","bounds":"inclusive"}`) || !strings.Contains(string(body), `"absolute_threshold":null`) {
		t.Fatalf("canonical metric JSON=%s err=%v", body, err)
	}

	changed := aggregate
	changedUpper, _ := formula.ParseDecimal("1.3")
	changed.Descriptor.TargetRange = &ValueRange{Lower: lower, Upper: changedUpper, Bounds: Inclusive}
	changedResult, err := NewCanonicalResult(strings.Repeat("a", 64), strings.Repeat("b", 64), []AggregateResult{changed}, nil)
	if err != nil {
		t.Fatal(err)
	}
	originalHash, _ := result.Hash()
	changedHash, _ := changedResult.Hash()
	if originalHash == changedHash {
		t.Fatal("target-range semantic change did not change result hash")
	}
}
