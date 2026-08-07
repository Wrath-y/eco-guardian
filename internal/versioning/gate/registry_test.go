package gate

import (
	"errors"
	"testing"

	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
)

func testDescriptor() Descriptor {
	return Descriptor{CapabilityID: "validation", GateID: "full", ContractVersion: "1", ImplementationVersion: "impl-1", RequiredInputs: []string{"revision_id", "config_hash"}, SupportedStates: []ResultState{Pass, Warning, Block, Unavailable, Stale}}
}

func TestRegistryRejectsInvalidAndDuplicateDescriptors(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	invalid := testDescriptor()
	invalid.RequiredInputs = nil
	if err := registry.Register(invalid); !errors.Is(err, ErrDescriptorInvalid) {
		t.Fatalf("invalid descriptor=%v", err)
	}
	if err := registry.Register(testDescriptor()); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(testDescriptor()); !errors.Is(err, ErrDescriptorDuplicate) {
		t.Fatalf("duplicate descriptor=%v", err)
	}
}

func TestRegistryPreservesDescriptorIdentityAndContractCompatibility(t *testing.T) {
	registry, err := NewRegistry(testDescriptor())
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := registry.Descriptor("validation", "full")
	if err != nil {
		t.Fatal(err)
	}
	descriptor.RequiredInputs[0] = "mutated"
	stored, err := registry.Descriptor("validation", "full")
	if err != nil || stored.RequiredInputs[0] != "revision_id" {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	if !registry.SupportsCapabilityContract(versioningpolicy.CapabilityRequirement{CapabilityID: "validation", GateID: "full", ContractVersion: "1", ImplementationVersion: "impl-1"}) {
		t.Fatal("matching contract rejected")
	}
	if registry.SupportsCapabilityContract(versioningpolicy.CapabilityRequirement{CapabilityID: "validation", GateID: "full", ContractVersion: "2"}) {
		t.Fatal("incompatible contract accepted")
	}
}
