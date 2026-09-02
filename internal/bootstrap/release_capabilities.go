package bootstrap

import (
	"github.com/zouyi/eco-guardian/internal/app"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	graphgate "github.com/zouyi/eco-guardian/internal/graph/gate"
	riskgate "github.com/zouyi/eco-guardian/internal/risk/gate"
	simulationcontract "github.com/zouyi/eco-guardian/internal/simulation/contract"
	simulationgate "github.com/zouyi/eco-guardian/internal/simulation/gate"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
)

// releaseCapabilitySet is process-owned registration data. It contains no
// project state and is therefore safe to construct once and share across
// Linux, macOS, and Windows project handles.
type releaseCapabilitySet struct {
	registry   *versioninggate.Registry
	simulation simulationgate.VersionContributor
	risk       riskgate.VersionContributor
}

func newReleaseCapabilitySet() (releaseCapabilitySet, error) {
	simulationRegistry, err := simulationcontract.NewManifestRegistry(simulationcontract.RequiredV1Descriptors, simulationcontract.V1Descriptors())
	if err != nil {
		return releaseCapabilitySet{}, err
	}
	simulationContributor, err := simulationgate.NewVersionContributor(simulationRegistry)
	if err != nil {
		return releaseCapabilitySet{}, err
	}
	riskContributor := riskgate.VersionContributor{Implementation: app.RiskImplementationVersion()}

	graphDescriptor := (graphgate.Provider{}).Descriptor()
	simulationDescriptor := (simulationgate.Provider{ImplementationVersion: simulationContributor.ImplementationVersion()}).Descriptor()
	riskDescriptor := (riskgate.Provider{Implementation: riskContributor.ImplementationVersion()}).Descriptor()
	backupDescriptor := currentBackupGateDescriptor()

	// The seeded v1 release policy predates the module-owned capability names.
	// Keep explicit compatibility descriptors for that public policy contract
	// while also registering the current module identities. The adapters share
	// the same implementation versions and never weaken supported states.
	descriptors := []versioninggate.Descriptor{
		graphDescriptor,
		simulationDescriptor,
		riskDescriptor,
		backupDescriptor,
		legacyGateDescriptor(graphDescriptor, "graph", "projection", "1"),
		legacyGateDescriptor(simulationDescriptor, "simulation", "scenario", "1"),
		legacyGateDescriptor(riskDescriptor, "risk", "threshold", "1"),
		legacyGateDescriptor(backupDescriptor, "backup", "online-backup", "1"),
	}
	registry, err := versioninggate.NewRegistry(descriptors...)
	if err != nil {
		return releaseCapabilitySet{}, err
	}
	return releaseCapabilitySet{registry: registry, simulation: simulationContributor, risk: riskContributor}, nil
}

func currentBackupGateDescriptor() versioninggate.Descriptor {
	descriptor := backupdomain.CurrentBackupCapabilityDescriptor()
	return versioninggate.Descriptor{
		CapabilityID: descriptor.CapabilityID, GateID: descriptor.GateID,
		ContractVersion: descriptor.ContractVersion, ImplementationVersion: descriptor.ImplementationVersion,
		RequiredInputs:          []string{"project_id", "release_job_id", "request_hash", "database_checksum", "integrity_result"},
		SupportedStates:         []versioninggate.ResultState{versioninggate.Pass, versioninggate.Block, versioninggate.Unavailable},
		OverridableNumericBlock: false,
	}
}

func legacyGateDescriptor(current versioninggate.Descriptor, capabilityID, gateID, contractVersion string) versioninggate.Descriptor {
	current.CapabilityID = capabilityID
	current.GateID = gateID
	current.ContractVersion = contractVersion
	return current
}
