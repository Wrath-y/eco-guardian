package gate

import versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"

const (
	CapabilityID          = "eco.graph.snapshot"
	GateID                = "eco.graph.snapshot.ready"
	ContractVersion       = "1"
	ImplementationVersion = "graph-gate-v1"
)

// Provider is the registration-only Graph capability. Evaluation remains at
// the Graph boundary so the #7 registry never imports local-rag or projection
// implementation packages.
type Provider struct{}

func (Provider) Descriptor() versioninggate.Descriptor {
	return versioninggate.Descriptor{
		CapabilityID:          CapabilityID,
		GateID:                GateID,
		ContractVersion:       ContractVersion,
		ImplementationVersion: ImplementationVersion,
		RequiredInputs: []string{
			"revision_id", "config_hash", "version_manifest_hash",
			"projection_schema_version", "projector_version",
			"graph_manifest_hash", "node_count", "edge_count",
			"provider_api_version", "provider_schema_version", "provider_capabilities",
			"graph_sync_result_identity",
		},
		SupportedStates:         []versioninggate.ResultState{versioninggate.Pass, versioninggate.Warning, versioninggate.Block, versioninggate.Unavailable, versioninggate.Stale},
		OverridableNumericBlock: false,
	}
}
