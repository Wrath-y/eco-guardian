package gate

import versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"

type DisabledReason struct {
	CapabilityID string `json:"capability_id"`
	GateID       string `json:"gate_id"`
	Reason       string `json:"reason"`
}

type ReleaseCapability struct {
	Enabled bool             `json:"enabled"`
	Reasons []DisabledReason `json:"reasons"`
}

// CalculateReleaseCapability reports registration completeness only. It does
// not run Gates or change revision/diff/validation behavior when a later
// capability is absent.
func CalculateReleaseCapability(registry *Registry, policy versioningpolicy.ReleasePolicy) ReleaseCapability {
	capability := ReleaseCapability{Enabled: true, Reasons: make([]DisabledReason, 0)}
	if !policy.Valid() {
		return ReleaseCapability{Reasons: []DisabledReason{{Reason: "invalid release policy"}}}
	}
	for _, requirement := range policy.Capabilities {
		reason := DisabledReason{CapabilityID: requirement.CapabilityID, GateID: requirement.GateID}
		if registry == nil {
			reason.Reason = "gate registry unavailable"
		} else if descriptor, err := registry.Descriptor(requirement.CapabilityID, requirement.GateID); err != nil {
			reason.Reason = "required gate is unregistered"
		} else if descriptor.ContractVersion != requirement.ContractVersion || (requirement.ImplementationVersion != "" && descriptor.ImplementationVersion != requirement.ImplementationVersion) {
			reason.Reason = "required gate contract is incompatible"
		}
		if reason.Reason != "" {
			capability.Enabled = false
			capability.Reasons = append(capability.Reasons, reason)
		}
	}
	return capability
}
