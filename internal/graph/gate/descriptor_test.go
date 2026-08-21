package gate

import (
	"testing"

	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
)

func TestGraphGateProviderRegistersStrictVersionedDescriptor(t *testing.T) {
	descriptor := (Provider{}).Descriptor()
	if !descriptor.Valid() || descriptor.CapabilityID != CapabilityID || descriptor.GateID != GateID || descriptor.ContractVersion != ContractVersion || descriptor.OverridableNumericBlock {
		t.Fatalf("descriptor=%#v", descriptor)
	}
	registry, err := versioninggate.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.RegisterProvider(Provider{}); err != nil {
		t.Fatal(err)
	}
	if err = registry.RegisterProvider(Provider{}); err == nil {
		t.Fatal("duplicate Graph Gate descriptor accepted")
	}
}
