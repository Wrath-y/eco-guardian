package sqlite

import (
	"testing"

	app "github.com/zouyi/eco-guardian/internal/app"
	riskgate "github.com/zouyi/eco-guardian/internal/risk/gate"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func TestRiskVersionContributorDefaultsUnavailableAndRegistersExactly(t *testing.T) {
	store := newStore(t)
	if store.riskVersion.CapabilityID != riskgate.CapabilityID || store.riskVersion.State != versioningrevision.Unregistered {
		t.Fatalf("default risk entry=%#v", store.riskVersion)
	}
	contributor := riskgate.VersionContributor{Implementation: app.RiskImplementationVersion()}
	if err := store.RegisterRiskVersionContributor(contributor); err != nil {
		t.Fatal(err)
	}
	if store.riskVersion.State != versioningrevision.Registered || store.riskVersion.ImplementationVersion != contributor.ImplementationVersion() {
		t.Fatalf("registered risk entry=%#v", store.riskVersion)
	}
}
