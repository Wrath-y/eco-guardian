package capability

import (
	"sort"
	"time"

	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

// EvaluateReleaseObservation adapts the existing Gate Registry, exact
// candidate evaluator, and policy assessment. Runtime never upgrades or
// relaxes any Gate result.
func EvaluateReleaseObservation(
	registry *versioninggate.Registry,
	policy versioningpolicy.ReleasePolicy,
	candidate versioningrevision.CandidateContext,
	results []versioninggate.Result,
	generation uint64,
	observedAt time.Time,
) Observation {
	reasons := []Reason{}
	state := Available
	registration := versioninggate.CalculateReleaseCapability(registry, policy)
	for _, disabled := range registration.Reasons {
		state = Unavailable
		code := releaseRegistrationCode(disabled.Reason)
		reasons = append(reasons, Reason{Code: code, Component: gateComponent(disabled.CapabilityID, disabled.GateID), ObservationGeneration: generation})
	}
	if policy.Valid() && candidate.Valid() {
		for _, requirement := range policy.Capabilities {
			var matched *versioninggate.Result
			for index := range results {
				if results[index].Descriptor.CapabilityID == requirement.CapabilityID && results[index].Descriptor.GateID == requirement.GateID {
					matched = &results[index]
					break
				}
			}
			component := gateComponent(requirement.CapabilityID, requirement.GateID)
			if matched == nil {
				state = Unavailable
				reasons = append(reasons, Reason{Code: "RELEASE_GATE_RESULT_MISSING", Component: component, ObservationGeneration: generation})
				continue
			}
			evaluated := versioninggate.EvaluateCandidate(candidate, policy, matched.Context, *matched)
			switch evaluated {
			case versioninggate.Stale:
				state = Unavailable
				reasons = append(reasons, Reason{Code: "RELEASE_GATE_STALE", Component: component, ObservationGeneration: generation})
			case versioninggate.Unavailable:
				state = Unavailable
				reasons = append(reasons, Reason{Code: "RELEASE_GATE_UNAVAILABLE", Component: component, ObservationGeneration: generation})
			case versioninggate.Block:
				state = Unavailable
				reasons = append(reasons, Reason{Code: "RELEASE_GATE_BLOCKED", Component: component, ObservationGeneration: generation})
			case versioninggate.Warning:
				if state == Available {
					state = Degraded
				}
				reasons = append(reasons, Reason{Code: "RELEASE_GATE_WARNING", Component: component, ObservationGeneration: generation})
			}
		}
		assessment := versioninggate.AssessPolicy(candidate, policy, results)
		for _, finding := range assessment.Findings {
			if finding.Required || finding.State != versioninggate.Warning {
				continue
			}
			if state == Available {
				state = Degraded
			}
			component := gateComponent(finding.CapabilityID, finding.GateID)
			if finding.SceneID != "" {
				component = "scene." + finding.SceneID + "." + finding.MetricID
			}
			reasons = append(reasons, Reason{Code: "OPTIONAL_GATE_WARNING", Component: component, ObservationGeneration: generation})
		}
	}
	sort.Slice(reasons, func(left, right int) bool {
		if reasons[left].Code != reasons[right].Code {
			return reasons[left].Code < reasons[right].Code
		}
		return reasons[left].Component < reasons[right].Component
	})
	return Observation{ID: ObservationReleaseGates, State: state, Generation: generation, ObservedAt: observedAt.UTC(), Reasons: reasons}
}

func releaseRegistrationCode(reason string) string {
	switch reason {
	case "required gate is unregistered":
		return "RELEASE_GATE_UNREGISTERED"
	case "required gate contract is incompatible":
		return "RELEASE_GATE_INCOMPATIBLE"
	case "gate registry unavailable":
		return "RELEASE_GATE_REGISTRY_UNAVAILABLE"
	default:
		return "RELEASE_POLICY_INVALID"
	}
}

func gateComponent(capabilityID, gateID string) string {
	if capabilityID == "" {
		return ObservationReleaseGates
	}
	if gateID == "" {
		return capabilityID
	}
	return capabilityID + "." + gateID
}
