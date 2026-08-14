package project

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

func TestSQLiteFactoryRunsConfiguredRecoveryOnProjectOpen(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	created, _, err := store.Create(context.Background(), directory, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Close(); err != nil {
		t.Fatal(err)
	}
	called := false
	factory := SQLiteFactory{Registry: registry, Recover: func(_ context.Context, opened *store.Store) error {
		called = opened.ProjectID().Valid()
		return nil
	}}
	handle, err := factory.Open(context.Background(), directory)
	if err != nil || !called {
		t.Fatalf("handle=%v called=%v err=%v", handle, called, err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteFactoryOptionallyRegistersGraphVersionContributor(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	descriptor := projector.Descriptor{SchemaVersion: projector.ProjectionSchemaV1, Version: projector.ProjectorV1, Relations: projector.V1Relations(), Formatter: projector.V1Formatter{}}
	factory := SQLiteFactory{Registry: registry, GraphVersionContributor: projector.VersionContributor{Descriptor: descriptor}}
	handle, err := factory.Create(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	opened := handle.(*sqliteHandle).Store()
	_, revision, err := opened.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "water", Name: "Water", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := opened.GetRevisionRecord(context.Background(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range record.Metadata.Manifest.Entries {
		if entry.CapabilityID == projector.CapabilityID && entry.ImplementationVersion == "1/v1" {
			return
		}
	}
	t.Fatalf("Graph contributor missing from manifest: %#v", record.Metadata.Manifest)
}
