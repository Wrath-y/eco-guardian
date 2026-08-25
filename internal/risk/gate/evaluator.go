package gate

import (
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/risk/orchestration"
)

type Context struct {
	Candidate                       orchestration.RevisionRef
	ActiveBaselineReleaseID         domain.ID
	Policy                          contract.Identity
	Threshold                       contract.Identity
	ThresholdEnabled                bool
	Implementations                 []contract.Identity
	SimulationResults               map[domain.ID]string
	Requirements                    []contract.PolicyRequirement
	EstablishBaselineConfirmed      bool
	NoBaselineRequiredFactsComplete bool
	Decision                        *orchestration.Report
}

type Evaluation struct {
	State            contract.GateState `json:"state"`
	ReportHash       string             `json:"report_hash"`
	ItemIDs          []string           `json:"item_ids"`
	Reasons          []string           `json:"reasons"`
	Overridable      bool               `json:"overridable"`
	DecisionValid    bool               `json:"decision_valid"`
	DecisionReportID domain.ID          `json:"decision_report_id,omitempty"`
}

func Evaluate(report orchestration.Report, current Context) Evaluation {
	result := Evaluation{State: contract.GatePass, ReportHash: report.ReportHash, ItemIDs: []string{}, Reasons: []string{}}
	if !report.Valid() || report.Kind != contract.CalculationReport {
		result.State = contract.GateUnavailable
		result.Reasons = append(result.Reasons, "RISK_REPORT_UNAVAILABLE")
		return result
	}
	nonOverridableBlock := false
	eligibleBlock := false
	promote := func(state contract.GateState, reason string, itemID string, nonOverridable bool) {
		if gateRank(state) > gateRank(result.State) {
			result.State = state
		}
		if reason != "" {
			result.Reasons = append(result.Reasons, reason)
		}
		if itemID != "" {
			result.ItemIDs = append(result.ItemIDs, itemID)
		}
		nonOverridableBlock = nonOverridableBlock || nonOverridable
	}
	if current.Candidate != report.Candidate {
		promote(contract.GateStale, "CANDIDATE_CHANGED", "", true)
	}
	if !current.ThresholdEnabled {
		promote(contract.GateBlock, "THRESHOLD_NOT_CONFIGURED", "", true)
	}
	projection := orchestration.ProjectReport(report, orchestration.CurrentContext{ActiveBaselineReleaseID: current.ActiveBaselineReleaseID, Policy: current.Policy, Threshold: current.Threshold, Implementations: current.Implementations, SimulationResults: current.SimulationResults})
	for _, reason := range projection.Reasons {
		switch {
		case strings.HasPrefix(reason, "IMPLEMENTATION_MISSING:"):
			promote(contract.GateUnavailable, reason, "", true)
		case strings.HasPrefix(reason, "IMPLEMENTATION_CHANGED:"), strings.HasPrefix(reason, "SIMULATION_CHANGED:"), reason == "BASELINE_CHANGED", reason == "POLICY_CHANGED", reason == "THRESHOLD_CHANGED":
			promote(contract.GateStale, reason, "", true)
		}
	}
	if report.Baseline.Kind == contract.NoBaseline {
		if current.ActiveBaselineReleaseID != "" {
			promote(contract.GateStale, "BASELINE_CHANGED", "", true)
		} else if !current.EstablishBaselineConfirmed {
			promote(contract.GateBlock, "ESTABLISH_BASELINE_CONFIRMATION_REQUIRED", "", true)
		}
		if !current.NoBaselineRequiredFactsComplete {
			promote(contract.GateUnavailable, "FIRST_RELEASE_REQUIRED_FACTS_MISSING", "", true)
		}
	} else if current.ActiveBaselineReleaseID == "" || report.Baseline.ReleaseID != current.ActiveBaselineReleaseID {
		promote(contract.GateStale, "BASELINE_CHANGED", "", true)
	}
	eligibleIDs := map[string]struct{}{}
	metricItems := map[string]contract.RiskItemV1{}
	for _, stored := range report.Items {
		if stored.Item.Kind == contract.MetricRiskItem && stored.Item.Metric != nil {
			metric := stored.Item.Metric
			metricItems[metric.SceneID+"\x00"+metric.SceneVersion+"\x00"+metric.MetricID+"\x00"+metric.MetricVersion] = stored.Item
		}
	}
	if report.Baseline.Kind == contract.BaselineCurrent {
		if len(current.Requirements) == 0 {
			promote(contract.GateUnavailable, "POLICY_REQUIREMENTS_UNAVAILABLE", "", true)
		}
		for _, requirement := range current.Requirements {
			key := requirement.SceneID + "\x00" + requirement.SceneVersion + "\x00" + requirement.MetricID + "\x00" + requirement.MetricVersion
			item, found := metricItems[key]
			if !requirement.Valid() || !found || item.Role != requirement.Role {
				if requirement.Role == contract.Optional {
					promote(contract.GateWarning, "OPTIONAL_ITEM_MISSING", key, false)
				} else {
					promote(contract.GateUnavailable, "REQUIRED_ITEM_MISSING", key, true)
				}
			}
		}
	}
	for _, stored := range report.Items {
		item := stored.Item
		switch item.Status {
		case contract.Stale:
			if item.Role == contract.Required {
				promote(contract.GateStale, "REQUIRED_ITEM_STALE", item.ID, true)
			} else {
				promote(contract.GateWarning, "OPTIONAL_ITEM_STALE", item.ID, false)
			}
		case contract.Unavailable:
			if item.Role == contract.Required {
				promote(contract.GateUnavailable, "REQUIRED_ITEM_UNAVAILABLE", item.ID, true)
			} else {
				promote(contract.GateWarning, "OPTIONAL_ITEM_UNAVAILABLE", item.ID, false)
			}
		case contract.NotComparable:
			if item.Role == contract.Required {
				promote(contract.GateBlock, "REQUIRED_ITEM_NOT_COMPARABLE", item.ID, true)
			} else {
				promote(contract.GateWarning, "OPTIONAL_ITEM_NOT_COMPARABLE", item.ID, false)
			}
		case contract.Comparable:
			if item.Severity == nil {
				promote(contract.GateUnavailable, "ITEM_SEVERITY_MISSING", item.ID, true)
				continue
			}
			switch *item.Severity {
			case contract.Block:
				if item.Kind == contract.MetricRiskItem && item.Override == contract.NumericOverrideEligible {
					eligibleBlock = true
					eligibleIDs[item.ID] = struct{}{}
					promote(contract.GateBlock, "NUMERIC_BLOCK", item.ID, false)
				} else {
					promote(contract.GateBlock, "NON_OVERRIDABLE_BLOCK", item.ID, true)
				}
			case contract.Warning:
				promote(contract.GateWarning, "RISK_WARNING", item.ID, false)
			}
		}
	}
	if current.Decision != nil {
		result.DecisionValid = validDecision(report, *current.Decision, eligibleIDs)
		if result.DecisionValid {
			result.DecisionReportID = current.Decision.ID
		} else {
			promote(contract.GateBlock, "DECISION_INVALID", "", true)
		}
	}
	result.Overridable = result.State == contract.GateBlock && eligibleBlock && !nonOverridableBlock
	sort.Strings(result.ItemIDs)
	result.ItemIDs = uniqueStrings(result.ItemIDs)
	sort.Strings(result.Reasons)
	result.Reasons = uniqueStrings(result.Reasons)
	return result
}

func validDecision(source, decision orchestration.Report, eligible map[string]struct{}) bool {
	if !decision.Valid() || decision.Kind != contract.DecisionReport || decision.SourceReportID != source.ID || decision.CalculationHash != source.CalculationHash || decision.EvidenceHash != source.EvidenceHash || decision.Candidate != source.Candidate || decision.Policy != source.Policy || decision.Threshold != source.Threshold || strings.TrimSpace(decision.Envelope.DecisionReason) == "" {
		return false
	}
	for _, itemID := range decision.Envelope.DecisionItemIDs {
		if _, found := eligible[itemID]; !found {
			return false
		}
	}
	return true
}

func gateRank(state contract.GateState) int {
	switch state {
	case contract.GateStale:
		return 5
	case contract.GateUnavailable:
		return 4
	case contract.GateBlock:
		return 3
	case contract.GateWarning:
		return 2
	case contract.GatePass:
		return 1
	default:
		return 0
	}
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
