package comparison

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/risk/threshold"
)

const RuleVersionV1 = "v1"

type Result struct {
	ID           string                        `json:"id"`
	Role         riskcontract.PolicyRole       `json:"role"`
	Status       riskcontract.ComparisonStatus `json:"comparison_status"`
	Severity     *riskcontract.Severity        `json:"severity"`
	PolicyEffect *riskcontract.Severity        `json:"policy_effect"`
	Reason       string                        `json:"reason"`
	SignedDelta  *string                       `json:"signed_delta"`
	RiskDelta    *string                       `json:"risk_delta"`
	RelativeRisk *string                       `json:"relative_risk"`
	Candidate    riskcontract.MetricEvidence   `json:"candidate"`
	Baseline     riskcontract.MetricEvidence   `json:"baseline"`
	Threshold    threshold.Entry               `json:"threshold"`
	Rule         riskcontract.Identity         `json:"rule"`
	Override     riskcontract.OverrideClass    `json:"override_classification"`
	EvidenceHash string                        `json:"evidence_hash"`
	Subject      riskcontract.Subject          `json:"subject"`
	CandidateRun RunRef                        `json:"candidate_run"`
	BaselineRun  RunRef                        `json:"baseline_run"`
}

type RunRef struct {
	ID         domain.ID `json:"id"`
	ResultHash string    `json:"result_hash"`
}

func (r RunRef) Valid() bool { return r.ID.Valid() && len(r.ResultHash) == 64 }

type Request struct {
	ID           string
	Role         riskcontract.PolicyRole
	Candidate    riskcontract.MetricEvidence
	Baseline     riskcontract.MetricEvidence
	Threshold    threshold.Entry
	Rule         riskcontract.Identity
	StaleReason  string
	Subject      riskcontract.Subject
	CandidateRun RunRef
	BaselineRun  RunRef
}

// AssessmentRequest contains the deterministic comparison facts shared by
// formal reports and advisory proposal previews. ContractValid lets each
// caller validate its own evidence-reference shape without changing numeric or
// severity semantics.
type AssessmentRequest struct {
	Role          riskcontract.PolicyRole
	Candidate     riskcontract.MetricEvidence
	Baseline      riskcontract.MetricEvidence
	Threshold     threshold.Entry
	StaleReason   string
	ContractValid bool
}

type Assessment struct {
	Status       riskcontract.ComparisonStatus `json:"comparison_status"`
	Severity     *riskcontract.Severity        `json:"severity"`
	PolicyEffect *riskcontract.Severity        `json:"policy_effect"`
	Reason       string                        `json:"reason"`
	SignedDelta  *string                       `json:"signed_delta"`
	RiskDelta    *string                       `json:"risk_delta"`
	RelativeRisk *string                       `json:"relative_risk"`
	Override     riskcontract.OverrideClass    `json:"override_classification"`
}

func (result Result) Item(ordinal int) (riskcontract.RiskItemV1, error) {
	metric := result.reportEvidence()
	item := riskcontract.RiskItemV1{ID: result.ID, Ordinal: ordinal, Kind: riskcontract.MetricRiskItem, Role: result.Role, Status: result.Status, Severity: result.Severity, Reason: result.Reason, Rule: result.Rule, Override: result.Override, EvidenceHash: result.EvidenceHash, Metric: &metric}
	if !item.Valid() {
		return riskcontract.RiskItemV1{}, fmt.Errorf("invalid comparison item")
	}
	return item, nil
}

func Compare(request Request) Result {
	result := Result{ID: request.ID, Role: request.Role, Candidate: cloneMetric(request.Candidate), Baseline: cloneMetric(request.Baseline), Threshold: request.Threshold, Rule: request.Rule, Override: riskcontract.NonOverridable, Subject: request.Subject, CandidateRun: request.CandidateRun, BaselineRun: request.BaselineRun}
	assessment := Assess(AssessmentRequest{
		Role:          request.Role,
		Candidate:     result.Candidate,
		Baseline:      result.Baseline,
		Threshold:     request.Threshold,
		StaleReason:   request.StaleReason,
		ContractValid: strings.TrimSpace(request.ID) != "" && request.Rule.Valid() && request.Subject.Valid() && request.CandidateRun.Valid() && request.BaselineRun.Valid(),
	})
	result.Status = assessment.Status
	result.Severity = assessment.Severity
	result.PolicyEffect = assessment.PolicyEffect
	result.Reason = assessment.Reason
	result.SignedDelta = assessment.SignedDelta
	result.RiskDelta = assessment.RiskDelta
	result.RelativeRisk = assessment.RelativeRisk
	result.Override = assessment.Override
	result.EvidenceHash = evidenceHash(result)
	return result
}

// Assess is the single pure implementation of comparison status, decimal
// deltas, threshold boundaries, severity, and override classification.
func Assess(request AssessmentRequest) Assessment {
	result := Assessment{Override: riskcontract.NonOverridable}
	if strings.TrimSpace(request.StaleReason) != "" {
		return unavailableAssessment(result, riskcontract.Stale, request.StaleReason, riskcontract.Block)
	}
	if (request.Role != riskcontract.Required && request.Role != riskcontract.Optional) || !request.ContractValid || !request.Threshold.Valid() {
		return unavailableAssessment(result, riskcontract.NotComparable, "COMPARISON_CONTRACT_INVALID", policyEffect(request.Role, riskcontract.NotComparable))
	}
	if !request.Candidate.Valid() || !request.Baseline.Valid() {
		return unavailableAssessment(result, riskcontract.NotComparable, "METRIC_EVIDENCE_INVALID", policyEffect(request.Role, riskcontract.NotComparable))
	}
	if request.Candidate.MetricID != request.Baseline.MetricID || request.Candidate.MetricVersion != request.Baseline.MetricVersion {
		return unavailableAssessment(result, riskcontract.NotComparable, "METRIC_IDENTITY_MISMATCH", policyEffect(request.Role, riskcontract.NotComparable))
	}
	if request.Threshold.MetricID != request.Candidate.MetricID || request.Threshold.MetricVersion != request.Candidate.MetricVersion {
		return unavailableAssessment(result, riskcontract.NotComparable, "THRESHOLD_SCOPE_MISMATCH", policyEffect(request.Role, riskcontract.NotComparable))
	}
	if request.Candidate.Unit != request.Baseline.Unit || request.Candidate.Unit != request.Threshold.Unit {
		return unavailableAssessment(result, riskcontract.NotComparable, "METRIC_UNIT_MISMATCH", policyEffect(request.Role, riskcontract.NotComparable))
	}
	if request.Candidate.Direction != request.Baseline.Direction || request.Candidate.Direction != request.Threshold.Direction || !sameTargetRange(request.Candidate.TargetRange, request.Baseline.TargetRange) {
		return unavailableAssessment(result, riskcontract.NotComparable, "METRIC_DIRECTION_MISMATCH", policyEffect(request.Role, riskcontract.NotComparable))
	}
	if request.Candidate.Status == riskcontract.MetricUnavailable || request.Baseline.Status == riskcontract.MetricUnavailable {
		return unavailableAssessment(result, riskcontract.Unavailable, "METRIC_UNAVAILABLE", policyEffect(request.Role, riskcontract.Unavailable))
	}
	candidate, _ := formula.ParseDecimal(*request.Candidate.Value)
	baseline, _ := formula.ParseDecimal(*request.Baseline.Value)
	signed, err := formula.Subtract(candidate, baseline)
	if err != nil {
		return unavailableAssessment(result, riskcontract.NotComparable, "DECIMAL_OPERATION_INVALID", policyEffect(request.Role, riskcontract.NotComparable))
	}
	riskDelta, err := directionalRiskDelta(request.Candidate.Direction, candidate, baseline, request.Candidate.TargetRange)
	if err != nil {
		return unavailableAssessment(result, riskcontract.NotComparable, "DECIMAL_OPERATION_INVALID", policyEffect(request.Role, riskcontract.NotComparable))
	}
	signedText, riskText := signed.String(), riskDelta.String()
	result.SignedDelta, result.RiskDelta = &signedText, &riskText
	zero, _ := formula.ParseDecimal("0")
	var relative *formula.Decimal
	if baseline.Compare(zero) != 0 {
		denominator, absErr := formula.Abs(baseline)
		if absErr != nil {
			return unavailableAssessment(result, riskcontract.NotComparable, "DECIMAL_OPERATION_INVALID", policyEffect(request.Role, riskcontract.NotComparable))
		}
		value, divideErr := formula.Divide(riskDelta, denominator)
		if divideErr != nil {
			return unavailableAssessment(result, riskcontract.NotComparable, "DECIMAL_OPERATION_INVALID", policyEffect(request.Role, riskcontract.NotComparable))
		}
		relative = &value
		text := value.String()
		result.RelativeRisk = &text
	} else if request.Threshold.Absolute == nil {
		return unavailableAssessment(result, riskcontract.NotComparable, "ZERO_BASELINE_WITHOUT_ABSOLUTE_THRESHOLD", policyEffect(request.Role, riskcontract.NotComparable))
	}
	severity, err := evaluateSeverity(relative, riskDelta, request.Threshold)
	if err != nil {
		return unavailableAssessment(result, riskcontract.NotComparable, "THRESHOLD_CONTRACT_INVALID", policyEffect(request.Role, riskcontract.NotComparable))
	}
	result.Status = riskcontract.Comparable
	result.Severity = &severity
	result.PolicyEffect = &severity
	result.Override = riskcontract.NumericOverrideEligible
	return result
}

func (result Result) reportEvidence() riskcontract.MetricComparisonEvidence {
	assumptions := append([]string(nil), result.Candidate.Assumptions...)
	assumptions = append(assumptions, result.Baseline.Assumptions...)
	sort.Strings(assumptions)
	deduplicated := assumptions[:0]
	for _, assumption := range assumptions {
		if len(deduplicated) == 0 || deduplicated[len(deduplicated)-1] != assumption {
			deduplicated = append(deduplicated, assumption)
		}
	}
	candidate := reportMetricValue(result.Candidate, result.CandidateRun)
	baselineValue := reportMetricValue(result.Baseline, result.BaselineRun)
	return riskcontract.MetricComparisonEvidence{SceneID: result.Threshold.SceneID, SceneVersion: result.Threshold.SceneVersion, MetricID: result.Candidate.MetricID, MetricVersion: result.Candidate.MetricVersion, Unit: result.Candidate.Unit, Direction: result.Candidate.Direction, TargetRange: result.Candidate.TargetRange, AbsoluteThreshold: result.Candidate.AbsoluteThreshold, Subject: result.Subject, Candidate: candidate, Baseline: &baselineValue, SignedDelta: result.SignedDelta, RiskDelta: result.RiskDelta, RelativeRisk: result.RelativeRisk, Assumptions: deduplicated}
}

func reportMetricValue(metric riskcontract.MetricEvidence, run RunRef) riskcontract.ReportMetricValue {
	return riskcontract.ReportMetricValue{Status: metric.Status, Value: metric.Value, ConfidenceLow: metric.ConfidenceLow, ConfidenceHigh: metric.ConfidenceHigh, Unavailable: metric.Unavailable, RunID: run.ID, ResultHash: run.ResultHash}
}

func CompareAll(requests []Request) []Result {
	results := make([]Result, 0, len(requests))
	for _, request := range requests {
		results = append(results, Compare(request))
	}
	sort.Slice(results, func(i, j int) bool { return results[i].ID < results[j].ID })
	return results
}

func directionalRiskDelta(direction riskcontract.MetricDirection, candidate, baseline formula.Decimal, target *riskcontract.TargetRangeValue) (formula.Decimal, error) {
	switch direction {
	case riskcontract.HigherIsRisk:
		return formula.Subtract(candidate, baseline)
	case riskcontract.LowerIsRisk:
		return formula.Subtract(baseline, candidate)
	case riskcontract.TargetRange:
		if target == nil || !target.Valid() {
			return formula.Decimal{}, fmt.Errorf("target range is invalid")
		}
		candidateDistance, err := rangeDistance(candidate, *target)
		if err != nil {
			return formula.Decimal{}, err
		}
		baselineDistance, err := rangeDistance(baseline, *target)
		if err != nil {
			return formula.Decimal{}, err
		}
		return formula.Subtract(candidateDistance, baselineDistance)
	default:
		return formula.Decimal{}, fmt.Errorf("direction is invalid")
	}
}

func rangeDistance(value formula.Decimal, target riskcontract.TargetRangeValue) (formula.Decimal, error) {
	lower, err := formula.ParseDecimal(target.Lower)
	if err != nil {
		return formula.Decimal{}, err
	}
	upper, err := formula.ParseDecimal(target.Upper)
	if err != nil {
		return formula.Decimal{}, err
	}
	zero, _ := formula.ParseDecimal("0")
	if value.Compare(lower) < 0 {
		return formula.Subtract(lower, value)
	}
	if value.Compare(upper) > 0 {
		return formula.Subtract(value, upper)
	}
	return zero, nil
}

func evaluateSeverity(relative *formula.Decimal, riskDelta formula.Decimal, entry threshold.Entry) (riskcontract.Severity, error) {
	block, warning := false, false
	if relative != nil {
		relativeWarning, warningValid := parseBoundary(entry.Relative.Warning)
		relativeBlock, blockValid := parseBoundary(entry.Relative.Block)
		if !warningValid || !blockValid {
			return "", fmt.Errorf("relative boundary invalid")
		}
		block = relative.Compare(relativeBlock) >= 0
		warning = relative.Compare(relativeWarning) >= 0
	}
	if entry.Absolute != nil {
		absoluteWarning, warningValid := parseBoundary(entry.Absolute.Warning)
		absoluteBlock, blockValid := parseBoundary(entry.Absolute.Block)
		if !warningValid || !blockValid {
			return "", fmt.Errorf("absolute boundary invalid")
		}
		block = block || riskDelta.Compare(absoluteBlock) >= 0
		warning = warning || riskDelta.Compare(absoluteWarning) >= 0
	}
	if block {
		return riskcontract.Block, nil
	}
	if warning {
		return riskcontract.Warning, nil
	}
	return riskcontract.Info, nil
}

func parseBoundary(value string) (formula.Decimal, bool) {
	decimal, err := formula.ParseDecimal(value)
	return decimal, err == nil
}

func sameTargetRange(left, right *riskcontract.TargetRangeValue) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func unavailableAssessment(result Assessment, status riskcontract.ComparisonStatus, reason string, effect riskcontract.Severity) Assessment {
	result.Status = status
	result.Reason = reason
	result.Severity = nil
	result.PolicyEffect = &effect
	result.Override = riskcontract.NonOverridable
	return result
}

func policyEffect(role riskcontract.PolicyRole, status riskcontract.ComparisonStatus) riskcontract.Severity {
	if status == riskcontract.Stale || role == riskcontract.Required {
		return riskcontract.Block
	}
	return riskcontract.Warning
}

func evidenceHash(result Result) string {
	copy := result
	copy.EvidenceHash = ""
	hash, _, err := riskcontract.CanonicalHash("eco-guardian/risk-comparison-evidence/v1", copy)
	if err != nil {
		return ""
	}
	return hash
}

func cloneMetric(metric riskcontract.MetricEvidence) riskcontract.MetricEvidence {
	copy := metric
	copy.Assumptions = append([]string(nil), metric.Assumptions...)
	if metric.TargetRange != nil {
		rangeCopy := *metric.TargetRange
		copy.TargetRange = &rangeCopy
	}
	if metric.Unavailable != nil {
		reason := *metric.Unavailable
		reason.Missing = append([]string(nil), metric.Unavailable.Missing...)
		copy.Unavailable = &reason
	}
	return copy
}
