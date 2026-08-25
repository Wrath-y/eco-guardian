package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type aiAdmissionVersion struct{ capability, contract, implementation string }

func (v aiAdmissionVersion) CapabilityID() string          { return v.capability }
func (v aiAdmissionVersion) ContractVersion() string       { return v.contract }
func (v aiAdmissionVersion) ImplementationVersion() string { return v.implementation }
func (v aiAdmissionVersion) RegistrationState() versioningrevision.RegistrationState {
	return versioningrevision.Registered
}

func TestResolveAIAdmissionSnapshotPinsMaterializedRevisionGraphTargetsAndNoBaseline(t *testing.T) {
	store := newStore(t)
	descriptor := projector.Descriptor{SchemaVersion: projector.ProjectionSchemaV1, Version: projector.ProjectorV1, Relations: projector.V1Relations(), Formatter: projector.V1Formatter{}}
	if err := store.RegisterGraphVersionContributor(projector.VersionContributor{Descriptor: descriptor}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterSimulationVersionContributor(aiAdmissionVersion{capability: "simulation-engine", contract: "simulation-v1", implementation: "engine-v1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterRiskVersionContributor(aiAdmissionVersion{capability: "risk", contract: "risk-v1", implementation: "risk-v1"}); err != nil {
		t.Fatal(err)
	}
	entity, revision, err := store.Create(context.Background(), domain.KindTag, tagDraft("aiadmission"))
	if err != nil {
		t.Fatal(err)
	}
	if err = store.RunFullValidation(context.Background(), revision.ID); err != nil {
		t.Fatal(err)
	}
	graphHash := strings.Repeat("d", 64)
	if _, err = store.InsertProjectionSummary(context.Background(), projector.Summary{
		ProjectID: string(store.ProjectID()), RevisionID: string(revision.ID), ConfigHash: revision.ConfigHash,
		SchemaVersion: projector.ProjectionSchemaV1, ProjectorVersion: projector.ProjectorV1, ManifestHash: graphHash,
		NodeCount: 1,
	}, `{"source":"test"}`); err != nil {
		t.Fatal(err)
	}
	if err = store.CreateGraphSyncState(context.Background(), graphsync.SyncState{RevisionID: string(revision.ID), Pipeline: graphsync.StateReady, Warnings: []string{}}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := store.ResolveAIAdmissionSnapshot(context.Background(), aiorchestration.AdmissionSelection{
		ProjectID: aicontract.ProjectID(store.ProjectID()), BaseRevisionID: aicontract.RevisionID(revision.ID),
		Targets: []aiorchestration.AdmissionTargetSelection{{EntityID: aicontract.EntityID(entity.ID), Paths: []aicontract.AllowedPath{{Path: "/payload/category", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Valid() || snapshot.Base.ConfigHash != aicontract.Hash(revision.ConfigHash) || snapshot.Base.GraphContentHash != aicontract.Hash(graphHash) {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if snapshot.Baseline.Kind != aicontract.BaselineNone {
		t.Fatalf("baseline=%#v", snapshot.Baseline)
	}
	if len(snapshot.Targets) != 1 || snapshot.Targets[0].EntityID != aicontract.EntityID(entity.ID) || snapshot.Targets[0].Kind != string(domain.KindTag) || snapshot.Targets[0].EntityVersion != entity.EntityVersion || len(snapshot.Targets[0].Paths) != 1 {
		t.Fatalf("targets=%#v", snapshot.Targets)
	}
	if len(snapshot.RequiredVersions) != 7 || !snapshot.Base.MaterializationHash.Valid() {
		t.Fatalf("versions=%#v materialization=%q", snapshot.RequiredVersions, snapshot.Base.MaterializationHash)
	}
	for _, invalidPath := range []aicontract.FieldPath{"/name", "/payload/missing"} {
		_, err = store.ResolveAIAdmissionSnapshot(context.Background(), aiorchestration.AdmissionSelection{
			ProjectID: aicontract.ProjectID(store.ProjectID()), BaseRevisionID: aicontract.RevisionID(revision.ID),
			Targets: []aiorchestration.AdmissionTargetSelection{{EntityID: aicontract.EntityID(entity.ID), Paths: []aicontract.AllowedPath{{Path: invalidPath, Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}}},
		})
		if !errors.Is(err, aiorchestration.ErrAdmissionScopeInvalid) {
			t.Fatalf("invalid path %q err=%v", invalidPath, err)
		}
	}
}
