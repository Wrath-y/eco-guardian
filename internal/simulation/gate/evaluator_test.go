package gate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type evidenceSourceFake struct{ runs []RunEvidence }

func (f evidenceSourceFake) SimulationEvidence(context.Context, domain.ID) ([]RunEvidence, error) {
	return append([]RunEvidence(nil), f.runs...), nil
}

func TestEvaluatePolicyRequiresExactImmutableSimulationEvidence(t *testing.T) {
	revisionID, _ := domain.NewID()
	policyID, _ := domain.NewID()
	runID, _ := domain.NewID()
	hash := strings.Repeat("a", 64)
	seed := uint64(11)
	candidate := versioningrevision.CandidateContext{RevisionID: revisionID, ConfigHash: hash, ManifestHash: hash, PolicyID: policyID}
	definition := versioningpolicy.Definition{Samples: 1000, ThresholdID: "threshold", ThresholdOn: true, Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: SimulationCapabilityID, GateID: SimulationGateID, ContractVersion: SimulationGateContract}}, Scenes: []versioningpolicy.Scene{{ID: "single-target-30s", Version: "v1", Seed: &seed, Required: true, Metrics: []versioningpolicy.Metric{{ID: "metric-dps", Required: true}}}}}
	policy := versioningpolicy.ReleasePolicy{Definition: definition, ID: policyID, DisplayVersion: 1, CanonicalHash: hash, CreatedAt: time.Now().UTC()}
	run := RunEvidence{ID: runID, RevisionID: revisionID, Input: contract.SimulationInputV1{SchemaVersion: "v1", RevisionID: contract.ID(revisionID), ConfigHash: hash, ManifestHash: hash, SceneID: "single-target-30s", SceneVersion: "v1", SampleCount: 1000, Seed: seed}, InputHash: hash, FingerprintHash: hash, ResultHash: hash, Reproducible: true, Metrics: []MetricEvidence{{ID: "metric-dps", Version: "v1", Status: "available"}}}
	results := EvaluatePolicy(candidate, policy, "implementation-v1", map[string]string{"numeric-policy": "numeric-v1"}, []RunEvidence{run})
	if len(results) != 1 || results[0].State != versioninggate.Pass || !results[0].Valid() || len(results[0].Evidence) != 1 || results[0].Evidence[0].ID != string(runID) {
		t.Fatalf("results=%#v", results)
	}
	for _, mutate := range []func(*RunEvidence){
		func(value *RunEvidence) { value.Reproducible = false },
		func(value *RunEvidence) { value.Input.Seed++ },
		func(value *RunEvidence) { value.Metrics[0].Status = "unavailable" },
		func(value *RunEvidence) { value.Input.SceneVersion = "v2" },
	} {
		copy := run
		copy.Metrics = append([]MetricEvidence(nil), run.Metrics...)
		mutate(&copy)
		result := EvaluatePolicy(candidate, policy, "implementation-v1", nil, []RunEvidence{copy})
		if len(result) != 1 || (result[0].State != versioninggate.Stale && result[0].State != versioninggate.Unavailable) || !result[0].Valid() {
			t.Fatalf("non-exact evidence result=%#v", result)
		}
	}
}

func TestResultSourceImplementsReadOnlyGateResultPort(t *testing.T) {
	id, _ := domain.NewID()
	policyID, _ := domain.NewID()
	hash := strings.Repeat("a", 64)
	seed := uint64(11)
	candidate := versioningrevision.CandidateContext{RevisionID: id, ConfigHash: hash, ManifestHash: hash, PolicyID: policyID}
	policy := versioningpolicy.ReleasePolicy{Definition: versioningpolicy.Definition{Samples: 1, ThresholdID: "threshold", ThresholdOn: true, Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: SimulationCapabilityID, GateID: SimulationGateID, ContractVersion: SimulationGateContract}}, Scenes: []versioningpolicy.Scene{{ID: "scene", Seed: &seed, Required: true, Metrics: []versioningpolicy.Metric{{ID: "metric", Required: true}}}}}, ID: policyID, DisplayVersion: 1, CanonicalHash: hash, CreatedAt: time.Now().UTC()}
	source := ResultSource{Evidence: evidenceSourceFake{}, ImplementationVersion: "simulation-v1"}
	results, err := source.Results(context.Background(), candidate, policy)
	if err != nil || len(results) != 1 || results[0].State != versioninggate.Unavailable || !results[0].Valid() {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	registry, err := versioninggate.NewRegistry()
	if err != nil || registry.RegisterProvider(Provider{ImplementationVersion: "simulation-v1"}) != nil {
		t.Fatalf("registry err=%v", err)
	}
}
