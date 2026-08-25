package gate

import (
	"strings"

	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

const (
	CapabilityID    = "risk"
	GateID          = "balance"
	ContractVersion = "risk-v1"
)

type VersionContributor struct{ Implementation string }

func (c VersionContributor) CapabilityID() string    { return CapabilityID }
func (c VersionContributor) ContractVersion() string { return ContractVersion }
func (c VersionContributor) ImplementationVersion() string {
	return c.Implementation
}
func (c VersionContributor) RegistrationState() versioningrevision.RegistrationState {
	if strings.TrimSpace(c.Implementation) == "" {
		return versioningrevision.Unregistered
	}
	return versioningrevision.Registered
}

type Provider struct{ Implementation string }

func (p Provider) Descriptor() versioninggate.Descriptor {
	return versioninggate.Descriptor{CapabilityID: CapabilityID, GateID: GateID, ContractVersion: ContractVersion, ImplementationVersion: p.Implementation, RequiredInputs: []string{"candidate_revision", "candidate_config_hash", "candidate_version_manifest", "baseline_release", "baseline_revision", "release_policy", "threshold_version", "validation_result", "simulation_runs", "metric_results", "risk_report", "report_schema", "comparison_rules", "cohort_rules", "structural_rules", "numeric_override_classification"}, SupportedStates: []versioninggate.ResultState{versioninggate.Pass, versioninggate.Warning, versioninggate.Block, versioninggate.Unavailable, versioninggate.Stale}, OverridableNumericBlock: true}
}

var _ versioningrevision.VersionContributor = VersionContributor{}
var _ versioninggate.Provider = Provider{}
