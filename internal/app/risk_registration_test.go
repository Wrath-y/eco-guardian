package app

import (
	"context"
	"testing"

	riskgate "github.com/zouyi/eco-guardian/internal/risk/gate"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type riskVersionSinkFake struct {
	entry versioningrevision.VersionEntry
}

type riskWorkerFake struct{ started, closed bool }

func (worker *riskWorkerFake) Start(context.Context) error { worker.started = true; return nil }
func (worker *riskWorkerFake) Close(context.Context) error { worker.closed = true; return nil }

func (sink *riskVersionSinkFake) RegisterRiskVersionContributor(contributor versioningrevision.VersionContributor) error {
	sink.entry = versioningrevision.VersionEntry{CapabilityID: contributor.CapabilityID(), ContractVersion: contributor.ContractVersion(), ImplementationVersion: contributor.ImplementationVersion(), State: contributor.RegistrationState()}
	return nil
}

func TestRiskRegistrationPinsContributorAndGateOrRecordsUnavailable(t *testing.T) {
	sink := &riskVersionSinkFake{}
	registry, err := versioninggate.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	worker := &riskWorkerFake{}
	integration, err := RegisterRiskIntegration(sink, registry, worker)
	if err != nil || integration.Contributor.RegistrationState() != versioningrevision.Registered || sink.entry.State != versioningrevision.Registered {
		t.Fatalf("integration=%#v entry=%#v err=%v", integration, sink.entry, err)
	}
	descriptor, err := registry.Descriptor(riskgate.CapabilityID, riskgate.GateID)
	if err != nil || descriptor.ImplementationVersion != RiskImplementationVersion() || !descriptor.OverridableNumericBlock {
		t.Fatalf("descriptor=%#v err=%v", descriptor, err)
	}
	if err = integration.Start(context.Background()); err != nil || !worker.started {
		t.Fatalf("worker start=%v err=%v", worker.started, err)
	}
	if err = integration.Close(context.Background()); err != nil || !worker.closed {
		t.Fatalf("worker close=%v err=%v", worker.closed, err)
	}

	absent := &riskVersionSinkFake{}
	if err = RegisterUnavailableRisk(absent); err != nil || absent.entry.State != versioningrevision.Unregistered || absent.entry.ImplementationVersion != "" {
		t.Fatalf("absent=%#v err=%v", absent.entry, err)
	}
	absentRegistry, err := versioninggate.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = absentRegistry.Descriptor(riskgate.CapabilityID, riskgate.GateID); err == nil {
		t.Fatal("absent risk unexpectedly registered a Gate")
	}
}
