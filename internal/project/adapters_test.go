package project

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	graphgate "github.com/zouyi/eco-guardian/internal/graph/gate"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type deterministicVersionContributor struct{ capability, contract, implementation string }

func (value deterministicVersionContributor) CapabilityID() string    { return value.capability }
func (value deterministicVersionContributor) ContractVersion() string { return value.contract }
func (value deterministicVersionContributor) ImplementationVersion() string {
	return value.implementation
}
func (value deterministicVersionContributor) RegistrationState() versioningrevision.RegistrationState {
	return versioningrevision.Registered
}

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

type projectGraphRecoveryFake struct{ resumed int }

func (f *projectGraphRecoveryFake) ResumeGraphJob(context.Context, graphsync.RecoveryWork) error {
	f.resumed++
	return nil
}
func (*projectGraphRecoveryFake) ReconcileInterruptedGraphJob(context.Context, graphsync.RecoveryWork) error {
	return nil
}

func TestSQLiteFactoryRunsGraphRecoveryForQueuedJobOnProjectOpen(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	created, _, err := store.Create(context.Background(), directory, registry)
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := created.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "recover", Name: "Recover", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := created.CreateOrGetGraphJob(context.Background(), graphsync.GraphJobRequest{ProjectID: created.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "graph-recover", RequestHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	if err = created.CreateGraphSyncState(context.Background(), graphsync.SyncState{RevisionID: string(revision.ID), Pipeline: graphsync.StateQueued, LatestJobID: string(job.ID), Warnings: []string{}}); err != nil {
		t.Fatal(err)
	}
	if err = created.Close(); err != nil {
		t.Fatal(err)
	}
	dispatcher := &projectGraphRecoveryFake{}
	handle, err := (SQLiteFactory{Registry: registry, GraphRecovery: dispatcher}).Open(context.Background(), directory)
	if err != nil || dispatcher.resumed != 1 {
		t.Fatalf("handle=%v resumed=%d err=%v", handle, dispatcher.resumed, err)
	}
	if err = handle.Close(); err != nil {
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

func TestSQLiteFactoryRegistersSimulationAndRiskVersions(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	factory := SQLiteFactory{
		Registry:                     registry,
		SimulationVersionContributor: deterministicVersionContributor{capability: "simulation-engine", contract: "simulation-v1", implementation: "simulation-implementation-v1"},
		RiskVersionContributor:       deterministicVersionContributor{capability: "risk", contract: "risk-v1", implementation: "risk-implementation-v1"},
	}
	handle, err := factory.Create(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	opened := handle.(*sqliteHandle).Store()
	_, revision, err := opened.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "versions", Name: "Versions", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := opened.GetRevisionRecord(context.Background(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"simulation-engine": "simulation-implementation-v1", "risk": "risk-implementation-v1"}
	for _, entry := range record.Metadata.Manifest.Entries {
		if implementation, found := want[entry.CapabilityID]; found && entry.State == versioningrevision.Registered && entry.ImplementationVersion == implementation {
			delete(want, entry.CapabilityID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("deterministic contributors missing from revision manifest: missing=%v manifest=%#v", want, record.Metadata.Manifest)
	}
}

func TestSQLiteFactoryOptionallyRegistersGraphGateOnceAcrossCreateAndOpen(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	gates, err := versioninggate.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	factory := SQLiteFactory{Registry: registry, GraphGateRegistry: gates, GraphGateProvider: graphgate.Provider{}}
	directory := t.TempDir()
	handle, err := factory.Create(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	if err = handle.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := factory.Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if descriptor, descriptorErr := gates.Descriptor(graphgate.CapabilityID, graphgate.GateID); descriptorErr != nil || descriptor.ContractVersion != graphgate.ContractVersion {
		t.Fatalf("descriptor=%#v err=%v", descriptor, descriptorErr)
	}
}

func TestGraphContributorStartsPostRevisionValidationPipeline(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	descriptor := projector.Descriptor{SchemaVersion: projector.ProjectionSchemaV1, Version: projector.ProjectorV1, Relations: projector.V1Relations(), Formatter: projector.V1Formatter{}}
	handle, err := (SQLiteFactory{Registry: registry, GraphVersionContributor: projector.VersionContributor{Descriptor: descriptor}}).Create(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	opened := handle.(*sqliteHandle).Store()
	_, revision, err := opened.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "pipeline", Name: "Pipeline", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	state, found, err := opened.GetGraphSyncState(context.Background(), revision.ID)
	if err != nil || !found || state.Pipeline != graphsync.StateQueued || state.LatestJobID == "" {
		t.Fatalf("state=%#v found=%v err=%v", state, found, err)
	}
	if job, err := opened.GetGraphJob(context.Background(), domain.ID(state.LatestJobID)); err != nil || job.RevisionID != revision.ID {
		t.Fatalf("job=%#v err=%v", job, err)
	}
}

func TestSQLiteFactoryInvokesRevisionObserverOnlyAfterSave(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	var observed domain.ID
	factory := SQLiteFactory{Registry: registry, AfterRevision: func(_ context.Context, opened *store.Store, revision domain.RevisionSummary) {
		if opened.ProjectID().Valid() {
			observed = revision.ID
		}
	}}
	handle, err := factory.Create(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	_, revision, err := handle.(*sqliteHandle).Store().Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "observed", Name: "Observed", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil || observed != revision.ID {
		t.Fatalf("observed=%s revision=%s err=%v", observed, revision.ID, err)
	}
}
