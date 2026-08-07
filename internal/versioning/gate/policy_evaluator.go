package gate

import (
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type Finding struct {
	CapabilityID string      `json:"capability_id,omitempty"`
	GateID       string      `json:"gate_id,omitempty"`
	SceneID      string      `json:"scene_id,omitempty"`
	MetricID     string      `json:"metric_id,omitempty"`
	Required     bool        `json:"required"`
	State        ResultState `json:"state"`
}

type Assessment struct {
	State    ResultState `json:"state"`
	Findings []Finding   `json:"findings"`
}

// AssessPolicy preserves the distinction between policy-required and optional
// analysis. It never invents a result for a missing optional Metric: that is a
// visible WARNING, while the same absence for a required input is a BLOCK.
func AssessPolicy(candidate versioningrevision.CandidateContext, policy versioningpolicy.ReleasePolicy, results []Result) Assessment {
	assessment := Assessment{State: Pass, Findings: make([]Finding, 0)}
	stateFor := func(required bool, result *Result) ResultState {
		if result == nil {
			if required {
				return Block
			}
			return Warning
		}
		state := EvaluateCandidate(candidate, policy, result.Context, *result)
		if required && (state == Unavailable || state == Stale) {
			return Block
		}
		if !required && (state == Unavailable || state == Stale) {
			return Warning
		}
		return state
	}
	add := func(finding Finding) {
		assessment.Findings = append(assessment.Findings, finding)
		if finding.State == Block {
			assessment.State = Block
		} else if finding.State == Warning && assessment.State == Pass {
			assessment.State = Warning
		}
	}
	for _, requirement := range policy.Capabilities {
		var matched *Result
		for index := range results {
			result := &results[index]
			if result.Descriptor.CapabilityID == requirement.CapabilityID && result.Descriptor.GateID == requirement.GateID {
				matched = result
				break
			}
		}
		add(Finding{CapabilityID: requirement.CapabilityID, GateID: requirement.GateID, Required: true, State: stateFor(true, matched)})
	}
	for _, scene := range policy.Scenes {
		for _, metric := range scene.Metrics {
			required := scene.Required && metric.Required
			var matched *Result
			for index := range results {
				result := &results[index]
				if result.Context.SceneID == scene.ID && result.Context.MetricID == metric.ID {
					matched = result
					break
				}
			}
			add(Finding{SceneID: scene.ID, MetricID: metric.ID, Required: required, State: stateFor(required, matched)})
		}
	}
	return assessment
}
