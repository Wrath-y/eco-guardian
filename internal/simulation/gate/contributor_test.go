package gate

import (
	"testing"

	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func TestVersionContributorPinsStableRegisteredManifest(t *testing.T) {
	registry, err := contract.NewManifestRegistry(contract.RequiredV1Descriptors, contract.V1Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	left, err := NewVersionContributor(registry)
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewVersionContributor(registry)
	if err != nil || left.CapabilityID() != SimulationCapabilityID || left.ContractVersion() != "simulation-v1" || len(left.ImplementationVersion()) != 64 || left.ImplementationVersion() != right.ImplementationVersion() || left.RegistrationState() != versioningrevision.Registered {
		t.Fatalf("left=%#v right=%#v err=%v", left, right, err)
	}
}
