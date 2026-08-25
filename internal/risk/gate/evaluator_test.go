package gate

import (
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/risk/orchestration"
)

const gateHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func gateMetricEvidence(withBaseline bool) *contract.MetricComparisonEvidence {
	value, low, high := "1", "0.9", "1.1"
	participant := domain.ID("01948c1e-0000-7000-8000-000000000071")
	evidence := &contract.MetricComparisonEvidence{SceneID: "scene", SceneVersion: "v1", MetricID: "metric-resource", MetricVersion: "v1", Unit: "ratio", Direction: contract.TargetRange, TargetRange: &contract.TargetRangeValue{Lower: "0.8", Upper: "1.2", Bounds: "inclusive"}, Subject: contract.Subject{Kind: contract.SingletonSubject, EntityKind: "character", StableID: participant, Members: []domain.ID{participant}}, Candidate: contract.ReportMetricValue{Status: contract.MetricAvailable, Value: &value, ConfidenceLow: &low, ConfidenceHigh: &high, RunID: "01948c1e-0000-7000-8000-000000000072", ResultHash: gateHash}, Assumptions: []string{"fixed fixture"}}
	if withBaseline {
		baseline := contract.ReportMetricValue{Status: contract.MetricAvailable, Value: &value, ConfidenceLow: &low, ConfidenceHigh: &high, RunID: "01948c1e-0000-7000-8000-000000000073", ResultHash: gateHash}
		evidence.Baseline = &baseline
		delta := "0"
		evidence.SignedDelta, evidence.RiskDelta, evidence.RelativeRisk = &delta, &delta, &delta
	}
	return evidence
}

func gateMetricItem(id string, role contract.PolicyRole, status contract.ComparisonStatus, severity *contract.Severity, override contract.OverrideClass, withBaseline bool) contract.RiskItemV1 {
	reason := ""
	if status != contract.Comparable {
		reason = "FIXTURE_STATUS"
	}
	return contract.RiskItemV1{ID: id, Kind: contract.MetricRiskItem, Role: role, Status: status, Severity: severity, Reason: reason, Rule: contract.Identity{ID: "metric-rule", Version: "v1", Hash: gateHash}, Override: override, EvidenceHash: gateHash, Metric: gateMetricEvidence(withBaseline)}
}

func gateStructuralBlock(id string) contract.RiskItemV1 {
	severity := contract.Block
	rule := contract.Identity{ID: "validation-static-cycle", Version: "v1", Hash: gateHash}
	evidence := &contract.StructureEvidence{Kind: contract.ValidationStructureIssue, Rule: rule, EntityID: "01948c1e-0000-7000-8000-000000000071", FieldPath: "/payload/formula", Fingerprint: gateHash, Override: contract.NonOverridable}
	return contract.RiskItemV1{ID: id, Kind: contract.StructuralRiskItem, Role: contract.Required, Status: contract.Comparable, Severity: &severity, Rule: rule, Override: contract.NonOverridable, EvidenceHash: gateHash, Structural: evidence}
}

func gateReport(t *testing.T, baseline contract.BaselineKind, items []contract.RiskItemV1) orchestration.Report {
	t.Helper()
	report := orchestration.Report{ID: "01948c1e-0000-7000-8000-000000000080", JobID: "01948c1e-0000-7000-8000-000000000081", ProjectID: "01948c1e-0000-7000-8000-000000000082", InputHash: gateHash, EvidenceHash: gateHash, Candidate: orchestration.RevisionRef{RevisionID: "01948c1e-0000-7000-8000-000000000083", ConfigHash: gateHash, ManifestHash: gateHash}, Baseline: orchestration.BaselineRef{Kind: baseline}, Policy: contract.Identity{ID: "01948c1e-0000-7000-8000-000000000084", Version: "1", Hash: gateHash}, Threshold: contract.Identity{ID: "01948c1e-0000-7000-8000-000000000085", Version: "1", Hash: gateHash}, Validation: contract.Identity{ID: "01948c1e-0000-7000-8000-000000000086", Version: "v1", Hash: gateHash}, Implementations: []contract.Identity{{ID: "risk-comparison", Version: "v1", Hash: gateHash}}, SimulationRuns: []orchestration.SimulationRef{{RunID: "01948c1e-0000-7000-8000-000000000072", InputHash: gateHash, FingerprintHash: gateHash, ResultHash: gateHash}}, CreatedAt: time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)}
	if baseline == contract.BaselineCurrent {
		report.Baseline.ReleaseID = "01948c1e-0000-7000-8000-000000000087"
		report.Baseline.Revision = &orchestration.RevisionRef{RevisionID: "01948c1e-0000-7000-8000-000000000088", ConfigHash: gateHash, ManifestHash: gateHash}
		report.SimulationRuns = append(report.SimulationRuns, orchestration.SimulationRef{RunID: "01948c1e-0000-7000-8000-000000000073", InputHash: gateHash, FingerprintHash: gateHash, ResultHash: gateHash})
	}
	value, err := orchestration.NewCalculationReport(report, items)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func exactGateContext(report orchestration.Report) Context {
	runs := map[domain.ID]string{}
	for _, run := range report.SimulationRuns {
		runs[run.RunID] = run.ResultHash
	}
	requirements := []contract.PolicyRequirement{}
	for _, item := range report.Items {
		if item.Item.Metric != nil {
			metric := item.Item.Metric
			requirements = append(requirements, contract.PolicyRequirement{SceneID: metric.SceneID, SceneVersion: metric.SceneVersion, MetricID: metric.MetricID, MetricVersion: metric.MetricVersion, Role: item.Item.Role})
		}
	}
	return Context{Candidate: report.Candidate, ActiveBaselineReleaseID: report.Baseline.ReleaseID, Policy: report.Policy, Threshold: report.Threshold, ThresholdEnabled: true, Implementations: append([]contract.Identity(nil), report.Implementations...), SimulationResults: runs, Requirements: requirements, EstablishBaselineConfirmed: true, NoBaselineRequiredFactsComplete: report.Baseline.Kind == contract.NoBaseline}
}

func TestLaterReleaseGateExactPassWarningsAndRequiredFailures(t *testing.T) {
	info, warning := contract.Info, contract.Warning
	tests := []struct {
		name string
		item contract.RiskItemV1
		want contract.GateState
	}{
		{"pass", gateMetricItem("info", contract.Required, contract.Comparable, &info, contract.NumericOverrideEligible, true), contract.GatePass},
		{"ordinary warning", gateMetricItem("warning", contract.Required, contract.Comparable, &warning, contract.NumericOverrideEligible, true), contract.GateWarning},
		{"optional unavailable", gateMetricItem("optional", contract.Optional, contract.Unavailable, nil, contract.NonOverridable, true), contract.GateWarning},
		{"required unavailable", gateMetricItem("required", contract.Required, contract.Unavailable, nil, contract.NonOverridable, true), contract.GateUnavailable},
		{"required not comparable", gateMetricItem("not-comparable", contract.Required, contract.NotComparable, nil, contract.NonOverridable, true), contract.GateBlock},
		{"required stale", gateMetricItem("stale", contract.Required, contract.Stale, nil, contract.NonOverridable, true), contract.GateStale},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := gateReport(t, contract.BaselineCurrent, []contract.RiskItemV1{test.item})
			result := Evaluate(report, exactGateContext(report))
			if result.State != test.want {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestGateRejectsThresholdBaselineRunAndImplementationDrift(t *testing.T) {
	info := contract.Info
	report := gateReport(t, contract.BaselineCurrent, []contract.RiskItemV1{gateMetricItem("info", contract.Required, contract.Comparable, &info, contract.NumericOverrideEligible, true)})
	tests := []struct {
		name   string
		mutate func(*Context)
		want   contract.GateState
		reason string
	}{
		{"threshold disabled", func(c *Context) { c.ThresholdEnabled = false }, contract.GateBlock, "THRESHOLD_NOT_CONFIGURED"},
		{"candidate changed", func(c *Context) { c.Candidate.ConfigHash = strings.Repeat("b", 64) }, contract.GateStale, "CANDIDATE_CHANGED"},
		{"policy changed", func(c *Context) { c.Policy.Hash = strings.Repeat("b", 64) }, contract.GateStale, "POLICY_CHANGED"},
		{"threshold changed", func(c *Context) { c.Threshold.Hash = strings.Repeat("b", 64) }, contract.GateStale, "THRESHOLD_CHANGED"},
		{"baseline changed", func(c *Context) { c.ActiveBaselineReleaseID = "01948c1e-0000-7000-8000-000000000099" }, contract.GateStale, "BASELINE_CHANGED"},
		{"run stale", func(c *Context) { c.SimulationResults[report.SimulationRuns[0].RunID] = strings.Repeat("b", 64) }, contract.GateStale, "SIMULATION_CHANGED:" + string(report.SimulationRuns[0].RunID)},
		{"implementation missing", func(c *Context) { c.Implementations = nil }, contract.GateUnavailable, "IMPLEMENTATION_MISSING:risk-comparison"},
		{"required metric missing", func(c *Context) {
			c.Requirements = append(c.Requirements, contract.PolicyRequirement{SceneID: "scene", SceneVersion: "v1", MetricID: "metric-missing", MetricVersion: "v1", Role: contract.Required})
		}, contract.GateUnavailable, "REQUIRED_ITEM_MISSING"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := exactGateContext(report)
			test.mutate(&current)
			result := Evaluate(report, current)
			if result.State != test.want || !containsReasonPrefix(result.Reasons, test.reason) {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestFirstReleaseRequiresNoBaselineFactsAndExplicitConfirmation(t *testing.T) {
	report := gateReport(t, contract.NoBaseline, nil)
	current := exactGateContext(report)
	current.EstablishBaselineConfirmed = false
	if result := Evaluate(report, current); result.State != contract.GateBlock || !containsReason(result.Reasons, "ESTABLISH_BASELINE_CONFIRMATION_REQUIRED") {
		t.Fatalf("unconfirmed=%#v", result)
	}
	current.EstablishBaselineConfirmed = true
	if result := Evaluate(report, current); result.State != contract.GatePass {
		t.Fatalf("confirmed=%#v", result)
	}
	current.NoBaselineRequiredFactsComplete = false
	if result := Evaluate(report, current); result.State != contract.GateUnavailable || !containsReason(result.Reasons, "FIRST_RELEASE_REQUIRED_FACTS_MISSING") {
		t.Fatalf("missing facts=%#v", result)
	}
	current.NoBaselineRequiredFactsComplete = true
	current.ActiveBaselineReleaseID = "01948c1e-0000-7000-8000-000000000087"
	if result := Evaluate(report, current); result.State != contract.GateStale {
		t.Fatalf("stale first release=%#v", result)
	}
}

func TestNumericDecisionRemainsBlockAndStructuralClassesNeverOverride(t *testing.T) {
	block := contract.Block
	numeric := gateReport(t, contract.BaselineCurrent, []contract.RiskItemV1{gateMetricItem("numeric", contract.Required, contract.Comparable, &block, contract.NumericOverrideEligible, true)})
	current := exactGateContext(numeric)
	result := Evaluate(numeric, current)
	if result.State != contract.GateBlock || !result.Overridable {
		t.Fatalf("numeric=%#v", result)
	}
	decisionBase := orchestration.Report{ID: "01948c1e-0000-7000-8000-000000000090", JobID: "01948c1e-0000-7000-8000-000000000091", ProjectID: numeric.ProjectID, InputHash: strings.Repeat("b", 64), CreatedAt: numeric.CreatedAt}
	decision, err := orchestration.NewDecisionReport(decisionBase, numeric, []string{"numeric"}, "accepted after deterministic review")
	if err != nil {
		t.Fatal(err)
	}
	current.Decision = &decision
	result = Evaluate(numeric, current)
	if result.State != contract.GateBlock || !result.Overridable || !result.DecisionValid || result.DecisionReportID != decision.ID {
		t.Fatalf("decision=%#v", result)
	}
	info := contract.Info
	structural := gateReport(t, contract.BaselineCurrent, []contract.RiskItemV1{gateMetricItem("info", contract.Required, contract.Comparable, &info, contract.NumericOverrideEligible, true), gateStructuralBlock("cycle")})
	result = Evaluate(structural, exactGateContext(structural))
	if result.State != contract.GateBlock || result.Overridable || !containsReason(result.Reasons, "NON_OVERRIDABLE_BLOCK") {
		t.Fatalf("structural=%#v", result)
	}
	for _, ruleID := range []string{"STATIC_FORMULA_CYCLE", "EVENT_LOOP_UNBOUNDED", "STACK_UNBOUNDED"} {
		item := gateStructuralBlock(ruleID)
		item.Rule.ID = ruleID
		item.Structural.Rule = item.Rule
		report := gateReport(t, contract.BaselineCurrent, []contract.RiskItemV1{gateMetricItem("info", contract.Required, contract.Comparable, &info, contract.NumericOverrideEligible, true), item})
		blocked := Evaluate(report, exactGateContext(report))
		if blocked.State != contract.GateBlock || blocked.Overridable {
			t.Fatalf("%s=%#v", ruleID, blocked)
		}
	}
}

func TestDescriptorDeclaresCompleteRiskEvidenceAndNumericOnlyOverride(t *testing.T) {
	descriptor := (Provider{Implementation: gateHash}).Descriptor()
	for _, required := range []string{"validation_result", "report_schema", "numeric_override_classification", "simulation_runs", "threshold_version"} {
		found := false
		for _, input := range descriptor.RequiredInputs {
			found = found || input == required
		}
		if !found {
			t.Fatalf("descriptor missing %s: %#v", required, descriptor)
		}
	}
	if !descriptor.Valid() || !descriptor.OverridableNumericBlock {
		t.Fatalf("descriptor=%#v", descriptor)
	}
}

func containsReason(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsReasonPrefix(values []string, want string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, want) {
			return true
		}
	}
	return false
}
