package revision

import "testing"

type contributor struct {
	id, contract, implementation string
	state                        RegistrationState
}

func (c contributor) CapabilityID() string                 { return c.id }
func (c contributor) ContractVersion() string              { return c.contract }
func (c contributor) ImplementationVersion() string        { return c.implementation }
func (c contributor) RegistrationState() RegistrationState { return c.state }

func TestRegistryRejectsMissingDuplicateAndIncompatibleContributors(t *testing.T) {
	entry := func(id string) VersionContributor {
		return contributor{id: id, contract: "1", implementation: id + "-v1", state: Registered}
	}
	base := []VersionContributor{entry("schema"), entry("dsl"), entry("validator-registry"), entry("numeric-policy")}
	if _, err := NewRegistry(map[string]string{"schema": "1"}, base); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(nil, base[:3]); err == nil {
		t.Fatal("missing required contributor accepted")
	}
	if _, err := NewRegistry(nil, append(base, entry("dsl"))); err == nil {
		t.Fatal("duplicate contributor accepted")
	}
	if _, err := NewRegistry(map[string]string{"schema": "2"}, base); err == nil {
		t.Fatal("incompatible contract accepted")
	}
}
