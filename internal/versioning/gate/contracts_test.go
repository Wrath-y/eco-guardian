package gate

import "testing"

type contractFake struct{ descriptor Descriptor }

func (f contractFake) Descriptor() Descriptor { return f.descriptor }

var _ GraphProvider = contractFake{}
var _ SimulationProvider = contractFake{}
var _ RiskProvider = contractFake{}
var _ BackupProvider = contractFake{}

func TestLaterCapabilityContractFakesRegisterWithoutBusinessImplementations(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range []Provider{
		contractFake{Descriptor{CapabilityID: "graph", GateID: "projection", ContractVersion: "1", ImplementationVersion: "fake", RequiredInputs: []string{"revision_id"}, SupportedStates: []ResultState{Pass, Block, Unavailable}}},
		contractFake{Descriptor{CapabilityID: "simulation", GateID: "scenario", ContractVersion: "1", ImplementationVersion: "fake", RequiredInputs: []string{"revision_id", "scene"}, SupportedStates: []ResultState{Pass, Block, Unavailable}}},
		contractFake{Descriptor{CapabilityID: "risk", GateID: "threshold", ContractVersion: "1", ImplementationVersion: "fake", RequiredInputs: []string{"revision_id", "threshold"}, SupportedStates: []ResultState{Pass, Warning, Block, Unavailable}}},
		contractFake{Descriptor{CapabilityID: "backup", GateID: "online-backup", ContractVersion: "1", ImplementationVersion: "fake", RequiredInputs: []string{"request_hash"}, SupportedStates: []ResultState{Pass, Block, Unavailable}}},
	} {
		if err := registry.RegisterProvider(provider); err != nil {
			t.Fatal(err)
		}
	}
}
