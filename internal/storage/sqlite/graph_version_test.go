package sqlite

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/graph/projector"
)

func TestGraphContributorPinsOnlyNewRevisionManifests(t *testing.T) {
	store := newStore(t)
	_, before, err := store.Create(context.Background(), "tag", tagDraft("beforegraph"))
	if err != nil {
		t.Fatal(err)
	}
	descriptor := projector.Descriptor{SchemaVersion: projector.ProjectionSchemaV1, Version: projector.ProjectorV1, Relations: projector.V1Relations(), Formatter: projector.V1Formatter{}}
	if err = store.RegisterGraphVersionContributor(projector.VersionContributor{Descriptor: descriptor}); err != nil {
		t.Fatal(err)
	}
	_, after, err := store.Create(context.Background(), "tag", tagDraft("aftergraph"))
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
	var beforeGraph, afterGraph string
	for _, entry := range beforeRecord.Metadata.Manifest.Entries {
		if entry.CapabilityID == projector.CapabilityID {
			beforeGraph = entry.ImplementationVersion
		}
	}
	for _, entry := range afterRecord.Metadata.Manifest.Entries {
		if entry.CapabilityID == projector.CapabilityID {
			afterGraph = entry.ImplementationVersion
		}
	}
	if beforeGraph != "" || afterGraph != "1/v1" {
		t.Fatalf("before=%q after=%q", beforeGraph, afterGraph)
	}
}
