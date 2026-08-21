package sqlite

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	simulationgate "github.com/zouyi/eco-guardian/internal/simulation/gate"
)

func TestSimulationContributorPinsOnlyNewRevisionManifests(t *testing.T) {
	store := newStore(t)
	_, before, err := store.Create(context.Background(), "tag", tagDraft("beforesimulation"))
	if err != nil {
		t.Fatal(err)
	}
	registry, err := contract.NewManifestRegistry(contract.RequiredV1Descriptors, contract.V1Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	contributor, err := simulationgate.NewVersionContributor(registry)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RegisterSimulationVersionContributor(contributor); err != nil {
		t.Fatal(err)
	}
	_, after, err := store.Create(context.Background(), "tag", tagDraft("aftersimulation"))
	if err != nil {
		t.Fatal(err)
	}
	beforeRecord, err := store.GetRevisionRecord(context.Background(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterRecord, err := store.GetRevisionRecord(context.Background(), after.ID)
	if err != nil {
		t.Fatal(err)
	}
	var beforeEntry, afterEntry string
	for _, entry := range beforeRecord.Metadata.Manifest.Entries {
		if entry.CapabilityID == simulationgate.SimulationCapabilityID {
			beforeEntry = entry.ImplementationVersion
		}
	}
	for _, entry := range afterRecord.Metadata.Manifest.Entries {
		if entry.CapabilityID == simulationgate.SimulationCapabilityID {
			afterEntry = entry.ImplementationVersion
		}
	}
	if beforeEntry != "" || afterEntry != contributor.ImplementationVersion() {
		t.Fatalf("before=%q after=%q", beforeEntry, afterEntry)
	}
}
