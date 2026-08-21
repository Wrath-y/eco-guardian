package metric

import (
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
