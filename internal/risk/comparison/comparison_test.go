package comparison

import (
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/risk/threshold"
)

func TestExactRelativeBoundariesAreBlockFirst(t *testing.T) {
	tests := []struct {
		candidate string
		want      riskcontract.Severity
	}{
		{"109.999", riskcontract.Info},
		{"110", riskcontract.Warning},
		{"124.999", riskcontract.Warning},
		{"125", riskcontract.Block},
	}
	for _, test := range tests {
		t.Run(test.candidate, func(t *testing.T) {
			result := Compare(compareRequest("item", riskcontract.Required, metric("metric", "points", riskcontract.HigherIsRisk, test.candidate), metric("metric", "points", riskcontract.HigherIsRisk, "100"), relativeEntry("metric", "points", riskcontract.HigherIsRisk)))
			if result.Status != riskcontract.Comparable || result.Severity == nil || *result.Severity != test.want {
				t.Fatalf("result=%#v", result)
			}
			if item, err := result.Item(0); err != nil || !item.Valid() || item.Status != riskcontract.Comparable || item.Kind != riskcontract.MetricRiskItem || item.Metric == nil || item.Metric.Candidate.RunID != result.CandidateRun.ID || item.Metric.Baseline == nil || item.Metric.SceneID != "scene" {
				t.Fatalf("item=%#v err=%v", item, err)
			}
		})
	}
}

func TestRiskDirectionSignedDeltaAndOppositeMovement(t *testing.T) {
	higher := Compare(compareRequest("higher", riskcontract.Required, metric("metric", "points", riskcontract.HigherIsRisk, "90"), metric("metric", "points", riskcontract.HigherIsRisk, "100"), relativeEntry("metric", "points", riskcontract.HigherIsRisk)))
	if *higher.SignedDelta != "-10" || *higher.RiskDelta != "-10" || *higher.Severity != riskcontract.Info {
		t.Fatalf("higher=%#v", higher)
	}
	lower := Compare(compareRequest("lower", riskcontract.Required, metric("metric", "points", riskcontract.LowerIsRisk, "90"), metric("metric", "points", riskcontract.LowerIsRisk, "100"), relativeEntry("metric", "points", riskcontract.LowerIsRisk)))
	if *lower.SignedDelta != "-10" || *lower.RiskDelta != "10" || *lower.RelativeRisk != "0.1" || *lower.Severity != riskcontract.Warning {
		t.Fatalf("lower=%#v", lower)
	}
	opposite := Compare(compareRequest("opposite", riskcontract.Required, metric("metric", "points", riskcontract.LowerIsRisk, "110"), metric("metric", "points", riskcontract.LowerIsRisk, "100"), relativeEntry("metric", "points", riskcontract.LowerIsRisk)))
	if *opposite.RiskDelta != "-10" || *opposite.Severity != riskcontract.Info {
		t.Fatalf("opposite=%#v", opposite)
	}
	negative := Compare(compareRequest("negative", riskcontract.Required, metric("metric", "points", riskcontract.HigherIsRisk, "-90"), metric("metric", "points", riskcontract.HigherIsRisk, "-100"), relativeEntry("metric", "points", riskcontract.HigherIsRisk)))
	if *negative.RelativeRisk != "0.1" || *negative.Severity != riskcontract.Warning {
		t.Fatalf("negative baseline=%#v", negative)
	}
}

func TestInclusiveTargetRangeUsesDistanceWithoutCrossingGuess(t *testing.T) {
	inside := Compare(targetRequest("inside", "1.2", "1"))
	if *inside.RiskDelta != "0" || *inside.Severity != riskcontract.Info {
		t.Fatalf("inside=%#v", inside)
	}
	outward := Compare(targetRequest("outward", "1.3", "1"))
	if *outward.RiskDelta != "0.1" || *outward.RelativeRisk != "0.1" || *outward.Severity != riskcontract.Warning {
		t.Fatalf("outward=%#v", outward)
	}
	inward := Compare(targetRequest("inward", "0.9", "0.7"))
	if *inward.RiskDelta != "-0.1" || *inward.Severity != riskcontract.Info {
		t.Fatalf("inward=%#v", inward)
	}
	crossing := Compare(targetRequest("crossing", "1.3", "0.7"))
	if *crossing.RiskDelta != "0" || *crossing.Severity != riskcontract.Info {
		t.Fatalf("crossing=%#v", crossing)
	}
}

func TestZeroBaselineNeverDividesAndRequiresAbsoluteBoundaries(t *testing.T) {
	request := compareRequest("zero", riskcontract.Required, metric("metric", "points", riskcontract.HigherIsRisk, "10"), metric("metric", "points", riskcontract.HigherIsRisk, "0"), relativeEntry("metric", "points", riskcontract.HigherIsRisk))
	result := Compare(request)
	if result.Status != riskcontract.NotComparable || result.Severity != nil || result.RelativeRisk != nil || result.Reason != "ZERO_BASELINE_WITHOUT_ABSOLUTE_THRESHOLD" || result.PolicyEffect == nil || *result.PolicyEffect != riskcontract.Block {
		t.Fatalf("zero=%#v", result)
	}
	absolute := threshold.Boundaries{Warning: "10", Block: "25"}
	request.Threshold.Absolute = &absolute
	result = Compare(request)
	if result.Status != riskcontract.Comparable || result.RelativeRisk != nil || *result.Severity != riskcontract.Warning {
		t.Fatalf("zero absolute warning=%#v", result)
	}
	candidate := "25"
	request.Candidate.Value, request.Candidate.ConfidenceLow, request.Candidate.ConfidenceHigh = &candidate, &candidate, &candidate
	result = Compare(request)
	if *result.Severity != riskcontract.Block {
		t.Fatalf("zero absolute block=%#v", result)
	}
}

func TestAbsoluteBoundariesAndConfidenceEvidenceDoNotProbabilisticallyAdjustSeverity(t *testing.T) {
	entry := relativeEntry("metric", "points", riskcontract.HigherIsRisk)
	absolute := threshold.Boundaries{Warning: "5", Block: "8"}
	entry.Absolute = &absolute
	request := compareRequest("absolute", riskcontract.Required, metric("metric", "points", riskcontract.HigherIsRisk, "108"), metric("metric", "points", riskcontract.HigherIsRisk, "100"), entry)
	first := Compare(request)
	if first.Severity == nil || *first.Severity != riskcontract.Block {
		t.Fatalf("absolute=%#v", first)
	}
	low, high := "1", "1000"
	request.Candidate.ConfidenceLow, request.Candidate.ConfidenceHigh = &low, &high
	second := Compare(request)
	if second.Severity == nil || *second.Severity != *first.Severity || second.EvidenceHash == first.EvidenceHash {
		t.Fatalf("CI altered severity or failed evidence capture: first=%#v second=%#v", first, second)
	}
}

func TestUnavailableRolesAndIndependentMetricsRemainSeparate(t *testing.T) {
	unavailable := metric("missing", "points", riskcontract.HigherIsRisk, "1")
	unavailable.Status = riskcontract.MetricUnavailable
	unavailable.Value, unavailable.ConfidenceLow, unavailable.ConfidenceHigh = nil, nil, nil
	unavailable.Unavailable = &riskcontract.UnavailableReason{Code: "MISSING", Missing: []string{"observation"}, Message: "structured observation unavailable"}
	required := Compare(compareRequest("required", riskcontract.Required, unavailable, metric("missing", "points", riskcontract.HigherIsRisk, "1"), relativeEntry("missing", "points", riskcontract.HigherIsRisk)))
	optional := Compare(compareRequest("optional", riskcontract.Optional, unavailable, metric("missing", "points", riskcontract.HigherIsRisk, "1"), relativeEntry("missing", "points", riskcontract.HigherIsRisk)))
	if required.Status != riskcontract.Unavailable || required.Severity != nil || *required.PolicyEffect != riskcontract.Block || optional.Status != riskcontract.Unavailable || optional.Severity != nil || *optional.PolicyEffect != riskcontract.Warning {
		t.Fatalf("required=%#v optional=%#v", required, optional)
	}
	good := compareRequest("good", riskcontract.Required, metric("good", "points", riskcontract.HigherIsRisk, "125"), metric("good", "points", riskcontract.HigherIsRisk, "100"), relativeEntry("good", "points", riskcontract.HigherIsRisk))
	results := CompareAll([]Request{good, compareRequest("missing", riskcontract.Optional, unavailable, metric("missing", "points", riskcontract.HigherIsRisk, "1"), relativeEntry("missing", "points", riskcontract.HigherIsRisk))})
	if len(results) != 2 || results[0].ID != "good" || *results[0].Severity != riskcontract.Block || results[1].Status != riskcontract.Unavailable {
		t.Fatalf("results=%#v", results)
	}
}

func TestUnitDirectionStaleAndDecimalContractMismatchesAreExplicit(t *testing.T) {
	base := compareRequest("item", riskcontract.Required, metric("metric", "points", riskcontract.HigherIsRisk, "110"), metric("metric", "points", riskcontract.HigherIsRisk, "100"), relativeEntry("metric", "points", riskcontract.HigherIsRisk))
	unit := base
	unit.Baseline.Unit = "seconds"
	if result := Compare(unit); result.Status != riskcontract.NotComparable || result.Reason != "METRIC_UNIT_MISMATCH" {
		t.Fatalf("unit=%#v", result)
	}
	direction := base
	direction.Threshold.Direction = riskcontract.LowerIsRisk
	if result := Compare(direction); result.Status != riskcontract.NotComparable || result.Reason != "METRIC_DIRECTION_MISMATCH" {
		t.Fatalf("direction=%#v", result)
	}
	stale := base
	stale.StaleReason = "RUN_RESULT_HASH_STALE"
	if result := Compare(stale); result.Status != riskcontract.Stale || result.Severity != nil || *result.PolicyEffect != riskcontract.Block {
		t.Fatalf("stale=%#v", result)
	}
	exponent := base
	exponentValue := "1e2"
	exponent.Candidate.Value, exponent.Candidate.ConfidenceLow, exponent.Candidate.ConfidenceHigh = &exponentValue, &exponentValue, &exponentValue
	if result := Compare(exponent); result.Status != riskcontract.NotComparable || result.Reason != "METRIC_EVIDENCE_INVALID" {
		t.Fatalf("exponent=%#v", result)
	}
	edge := base
	edgeValue := "100.0000000000000000000000000000001"
	edge.Candidate.Value, edge.Candidate.ConfidenceLow, edge.Candidate.ConfidenceHigh = &edgeValue, &edgeValue, &edgeValue
	if result := Compare(edge); result.Status != riskcontract.Comparable || result.Severity == nil || *result.Severity != riskcontract.Info {
		t.Fatalf("decimal128 edge=%#v", result)
	}
}

func compareRequest(id string, role riskcontract.PolicyRole, candidate, baseline riskcontract.MetricEvidence, entry threshold.Entry) Request {
	hash := strings.Repeat("a", 64)
	participant := domain.ID("01948c1e-0000-7000-8000-000000000031")
	return Request{ID: id, Role: role, Candidate: candidate, Baseline: baseline, Threshold: entry, Rule: riskcontract.Identity{ID: "risk-comparison-" + string(candidate.Direction), Version: RuleVersionV1, Hash: hash}, Subject: riskcontract.Subject{Kind: riskcontract.SingletonSubject, EntityKind: "character", StableID: participant, Members: []domain.ID{participant}}, CandidateRun: RunRef{ID: "01948c1e-0000-7000-8000-000000000032", ResultHash: hash}, BaselineRun: RunRef{ID: "01948c1e-0000-7000-8000-000000000033", ResultHash: hash}}
}

func metric(id, unit string, direction riskcontract.MetricDirection, value string) riskcontract.MetricEvidence {
	hash := strings.Repeat("a", 64)
	low, high := value, value
	return riskcontract.MetricEvidence{MetricID: id, MetricVersion: "v1", Status: riskcontract.MetricAvailable, Unit: unit, Direction: direction, Value: &value, ConfidenceLow: &low, ConfidenceHigh: &high, SampleCount: 1000, Assumptions: []string{"fixed fixture"}, CanonicalHash: hash}
}

func relativeEntry(metricID, unit string, direction riskcontract.MetricDirection) threshold.Entry {
	return threshold.Entry{SceneID: "scene", SceneVersion: "v1", MetricID: metricID, MetricVersion: "v1", Unit: unit, Direction: direction, Relative: threshold.Boundaries{Warning: "0.10", Block: "0.25"}}
}

func targetRequest(id, candidateValue, baselineValue string) Request {
	target := &riskcontract.TargetRangeValue{Lower: "0.8", Upper: "1.2", Bounds: "inclusive"}
	candidate := metric("metric-resource", "ratio", riskcontract.TargetRange, candidateValue)
	baseline := metric("metric-resource", "ratio", riskcontract.TargetRange, baselineValue)
	candidate.TargetRange, baseline.TargetRange = target, target
	return compareRequest(id, riskcontract.Required, candidate, baseline, relativeEntry("metric-resource", "ratio", riskcontract.TargetRange))
}
