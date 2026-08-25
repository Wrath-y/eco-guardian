package contract

import (
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestTaggedContractsRejectIllegalCombinations(t *testing.T) {
	hash := strings.Repeat("a", 64)
	identity := Identity{ID: "threshold", Version: "v1", Hash: hash}
	if !(ThresholdSelection{Kind: ExistingThreshold, Threshold: &identity}).Valid() {
		t.Fatal("valid existing threshold selection rejected")
	}
	if (ThresholdSelection{Kind: StarterThreshold}).Valid() {
		t.Fatal("unconfirmed starter threshold accepted")
	}
	if (ThresholdSelection{Kind: ExistingThreshold, Threshold: &identity, Confirmed: true}).Valid() {
		t.Fatal("existing threshold accepted an activation confirmation")
	}

	value := "1"
	low := "0.9"
	high := "1.1"
	metric := MetricEvidence{MetricID: "metric", MetricVersion: "v1", Status: MetricAvailable, Unit: "ratio", Direction: HigherIsRisk, Value: &value, ConfidenceLow: &low, ConfidenceHigh: &high, SampleCount: 10, CanonicalHash: hash}
	if !metric.Valid() {
		t.Fatal("valid available Metric rejected")
	}
	metric.Status = MetricUnavailable
	if metric.Valid() {
		t.Fatal("unavailable Metric accepted values")
	}
	metric.Value, metric.ConfidenceLow, metric.ConfidenceHigh = nil, nil, nil
	metric.Unavailable = &UnavailableReason{Code: "MISSING", Message: "Metric result is unavailable", Missing: []string{"result"}}
	if !metric.Valid() {
		t.Fatal("valid unavailable Metric rejected")
	}

	evidence := StructureEvidence{Kind: ValidationStructureIssue, Rule: Identity{ID: "static-cycle", Version: "v1", Hash: hash}, EntityID: domain.ID("01948c1e-0000-7000-8000-000000000001"), FieldPath: "/payload/formula", Fingerprint: hash, Override: NonOverridable}
	if !evidence.Valid() {
		t.Fatal("valid structure evidence rejected")
	}
	evidence.Override = NumericOverrideEligible
	if evidence.Valid() {
		t.Fatal("structural evidence accepted numeric override")
	}

	if !(GateResult{State: GatePass, ReportHash: hash}).Valid() {
		t.Fatal("valid PASS Gate result rejected")
	}
	if (GateResult{State: GatePass, ReportHash: hash, ItemIDs: []string{"item"}, Reason: "blocked"}).Valid() {
		t.Fatal("PASS Gate result accepted blocking evidence")
	}
	if !(GateResult{State: GateBlock, ReportHash: hash, ItemIDs: []string{"item"}, Reason: "required item blocked"}).Valid() {
		t.Fatal("valid BLOCK Gate result rejected")
	}
}

func TestTargetRangeRequiresCanonicalInclusiveBounds(t *testing.T) {
	if !(TargetRangeValue{Lower: "0.8", Upper: "1.2", Bounds: "inclusive"}).Valid() {
		t.Fatal("canonical inclusive range rejected")
	}
	for _, invalid := range []TargetRangeValue{
		{Lower: "0.80", Upper: "1.2", Bounds: "inclusive"},
		{Lower: "1.2", Upper: "0.8", Bounds: "inclusive"},
		{Lower: "0.8", Upper: "1.2", Bounds: "exclusive"},
	} {
		if invalid.Valid() {
			t.Fatalf("invalid target range accepted: %#v", invalid)
		}
	}
}
