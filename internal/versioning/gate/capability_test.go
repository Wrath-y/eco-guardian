package gate

import (
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
)

func TestCalculateReleaseCapabilityDisablesOnlyReleaseForMissingOrIncompatibleGates(t *testing.T) {
	policy := capabilityPolicy(t)
	disabledThreshold := policy
	disabledThreshold.ThresholdOn = false
	if capability := CalculateReleaseCapability(nil, disabledThreshold); capability.Enabled || len(capability.Reasons) != 1 || capability.Reasons[0].Reason != "invalid release policy" {
		t.Fatalf("disabled threshold=%#v", capability)
	}
	missing := CalculateReleaseCapability(nil, policy)
	if missing.Enabled || len(missing.Reasons) != 2 {
		t.Fatalf("missing=%#v", missing)
	}
	registry, err := NewRegistry(Descriptor{CapabilityID: "graph", GateID: "projection", ContractVersion: "2", ImplementationVersion: "impl", RequiredInputs: []string{"revision_id"}, SupportedStates: []ResultState{Pass, Block, Unavailable}})
	if err != nil {
		t.Fatal(err)
	}
	incompatible := CalculateReleaseCapability(registry, policy)
	if incompatible.Enabled || len(incompatible.Reasons) != 2 || incompatible.Reasons[0].Reason != "required gate contract is incompatible" {
		t.Fatalf("incompatible=%#v", incompatible)
	}
	if err := registry.Register(Descriptor{CapabilityID: "risk", GateID: "threshold", ContractVersion: "1", ImplementationVersion: "impl", RequiredInputs: []string{"revision_id"}, SupportedStates: []ResultState{Pass, Block, Unavailable}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Descriptor{CapabilityID: "graph", GateID: "projection", ContractVersion: "1", ImplementationVersion: "impl", RequiredInputs: []string{"revision_id"}, SupportedStates: []ResultState{Pass, Block, Unavailable}}); err == nil {
		t.Fatal("duplicate identity should remain incompatible")
	}
	compatible, err := NewRegistry(Descriptor{CapabilityID: "graph", GateID: "projection", ContractVersion: "1", ImplementationVersion: "impl", RequiredInputs: []string{"revision_id"}, SupportedStates: []ResultState{Pass, Block, Unavailable}}, Descriptor{CapabilityID: "risk", GateID: "threshold", ContractVersion: "1", ImplementationVersion: "impl", RequiredInputs: []string{"revision_id"}, SupportedStates: []ResultState{Pass, Block, Unavailable}})
	if err != nil {
		t.Fatal(err)
	}
	if enabled := CalculateReleaseCapability(compatible, policy); !enabled.Enabled || len(enabled.Reasons) != 0 {
		t.Fatalf("enabled=%#v", enabled)
	}
}

func capabilityPolicy(t *testing.T) versioningpolicy.ReleasePolicy {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	definition := versioningpolicy.Definition{Samples: 1, ThresholdID: "threshold", ThresholdOn: true, Scenes: []versioningpolicy.Scene{{ID: "scene", Metrics: []versioningpolicy.Metric{{ID: "metric", Required: true}}}}, Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: "graph", GateID: "projection", ContractVersion: "1"}, {CapabilityID: "risk", GateID: "threshold", ContractVersion: "1"}}}
	policy := versioningpolicy.ReleasePolicy{Definition: definition, ID: id, DisplayVersion: 1, CreatedAt: time.Now()}
	hash, err := policy.Hash()
	if err != nil {
		t.Fatal(err)
	}
	policy.CanonicalHash = hash
	return policy
}
