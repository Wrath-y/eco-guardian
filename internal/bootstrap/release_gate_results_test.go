package bootstrap

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func TestReleaseGateResultsExposeUnavailableEvidenceWithoutInventingPasses(t *testing.T) {
	capabilities, err := newReleaseCapabilitySet()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	projectStore, _, err := store.Create(context.Background(), t.TempDir(), registry)
	if err != nil {
		t.Fatal(err)
	}
	defer projectStore.Close()
	if err = projectStore.RegisterSimulationVersionContributor(capabilities.simulation); err != nil {
		t.Fatal(err)
	}
	if err = projectStore.RegisterRiskVersionContributor(capabilities.risk); err != nil {
		t.Fatal(err)
	}
	_, revision, err := projectStore.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "release_gate", Name: "Release Gate", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	record, err := projectStore.GetRevisionRecord(context.Background(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	policies, err := projectStore.ListPolicies(context.Background(), "", 1)
	if err != nil || len(policies.Items) != 1 {
		t.Fatalf("policies=%#v err=%v", policies, err)
	}
	policy := policies.Items[0]
	candidate := versioningrevision.CandidateContext{RevisionID: revision.ID, ConfigHash: record.Metadata.ConfigHash, ManifestHash: record.Metadata.ManifestHash, PolicyID: policy.ID}
	results, err := (releaseGateResults{
		registry: capabilities.registry, store: projectStore, simulationVersion: capabilities.simulation.ImplementationVersion(),
		backupAvailable: func(context.Context) bool { return true },
	}).Results(context.Background(), candidate, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < len(policy.Capabilities) {
		t.Fatalf("results=%d policy capabilities=%d", len(results), len(policy.Capabilities))
	}
	states := map[string]versioninggate.ResultState{}
	for _, result := range results {
		if !result.Valid() {
			t.Fatalf("invalid Gate result: %#v", result)
		}
		states[result.Descriptor.CapabilityID] = result.State
	}
	if states["backup"] != versioninggate.Pass || states["graph"] != versioninggate.Unavailable || states["risk"] != versioninggate.Unavailable || states["simulation"] != versioninggate.Unavailable {
		t.Fatalf("unexpected Gate states: %v", states)
	}
	if assessment := versioninggate.AssessPolicy(candidate, policy, results); assessment.State != versioninggate.Block {
		t.Fatalf("missing immutable evidence was not blocking: %#v", assessment)
	}
}
