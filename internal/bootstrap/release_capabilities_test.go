package bootstrap

import (
	"testing"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	graphgate "github.com/zouyi/eco-guardian/internal/graph/gate"
	riskgate "github.com/zouyi/eco-guardian/internal/risk/gate"
	simulationgate "github.com/zouyi/eco-guardian/internal/simulation/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
)

func TestReleaseCapabilitySetRegistersCurrentAndSeededPolicyContracts(t *testing.T) {
	capabilities, err := newReleaseCapabilitySet()
	if err != nil {
		t.Fatal(err)
	}
	currentBackup := backupdomain.CurrentBackupCapabilityDescriptor()
	requirements := []versioningpolicy.CapabilityRequirement{
		{CapabilityID: graphgate.CapabilityID, GateID: graphgate.GateID, ContractVersion: graphgate.ContractVersion},
		{CapabilityID: simulationgate.SimulationCapabilityID, GateID: simulationgate.SimulationGateID, ContractVersion: simulationgate.SimulationGateContract},
		{CapabilityID: riskgate.CapabilityID, GateID: riskgate.GateID, ContractVersion: riskgate.ContractVersion},
		{CapabilityID: currentBackup.CapabilityID, GateID: currentBackup.GateID, ContractVersion: currentBackup.ContractVersion},
		{CapabilityID: "graph", GateID: "projection", ContractVersion: "1"},
		{CapabilityID: "simulation", GateID: "scenario", ContractVersion: "1"},
		{CapabilityID: "risk", GateID: "threshold", ContractVersion: "1"},
		{CapabilityID: "backup", GateID: "online-backup", ContractVersion: "1"},
	}
	for _, requirement := range requirements {
		if !capabilities.registry.SupportsCapabilityContract(requirement) {
			t.Errorf("release contract was not registered: %#v", requirement)
		}
	}
	if capabilities.simulation.RegistrationState() != "registered" || capabilities.risk.RegistrationState() != "registered" {
		t.Fatalf("deterministic contributors are unavailable: simulation=%s risk=%s", capabilities.simulation.RegistrationState(), capabilities.risk.RegistrationState())
	}
}
