package gate

import (
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func TestEvaluateCandidateRejectsEveryMismatchedIdentityAsStale(t *testing.T) {
	revisionID, policyID, resultID := mustGateID(t), mustGateID(t), mustGateID(t)
	definition := versioningpolicy.Definition{Samples: 1, ThresholdID: "threshold-v1", ThresholdOn: true, Scenes: []versioningpolicy.Scene{{ID: "scene", Metrics: []versioningpolicy.Metric{{ID: "metric", Required: true}}}}, Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: "risk", GateID: "threshold", ContractVersion: "1"}}}
	policy := versioningpolicy.ReleasePolicy{Definition: definition, ID: policyID, DisplayVersion: 1, CreatedAt: time.Now()}
	hash, err := policy.Hash()
	if err != nil {
		t.Fatal(err)
	}
	policy.CanonicalHash = hash
	candidate := versioningrevision.CandidateContext{RevisionID: revisionID, ConfigHash: strings.Repeat("a", 64), ManifestHash: strings.Repeat("b", 64), PolicyID: policyID}
	context := EvaluationContext{Candidate: candidate, PolicyHash: hash, SceneID: "scene", MetricID: "metric", ThresholdID: "threshold-v1", ImplementationVersions: map[string]string{"risk": "risk-v1"}}
	descriptor := Descriptor{CapabilityID: "risk", GateID: "threshold", ContractVersion: "1", ImplementationVersion: "risk-v1", RequiredInputs: []string{"revision_id"}, SupportedStates: []ResultState{Pass, Warning, Block, Stale}}
	result := Result{ID: resultID, Descriptor: descriptor, State: Pass, ResultHash: strings.Repeat("c", 64), Context: context}
	if got := EvaluateCandidate(candidate, policy, context, result); got != Pass {
		t.Fatalf("matching result=%s", got)
	}
	mutations := []func(*EvaluationContext){
		func(c *EvaluationContext) { c.Candidate.RevisionID = mustGateID(t) },
		func(c *EvaluationContext) { c.Candidate.ConfigHash = strings.Repeat("d", 64) },
		func(c *EvaluationContext) { c.Candidate.ManifestHash = strings.Repeat("e", 64) },
		func(c *EvaluationContext) { c.Candidate.PolicyID = mustGateID(t) },
		func(c *EvaluationContext) { c.Candidate.BaselineReleaseID = mustGateID(t) },
		func(c *EvaluationContext) { c.PolicyHash = strings.Repeat("f", 64) },
		func(c *EvaluationContext) { c.SceneID = "other" },
		func(c *EvaluationContext) { c.MetricID = "other" },
		func(c *EvaluationContext) { c.ThresholdID = "other" },
		func(c *EvaluationContext) { c.ImplementationVersions["risk"] = "risk-v2" },
	}
	for index, mutate := range mutations {
		mutated := cloneContext(context)
		mutate(&mutated)
		staleResult := result
		staleResult.Context = mutated
		if got := EvaluateCandidate(candidate, policy, context, staleResult); got != Stale {
			t.Fatalf("mutation %d state=%s", index, got)
		}
	}
}

func mustGateID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
