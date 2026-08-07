package gate

import (
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func TestAssessPolicyBlocksRequiredAndWarnsOptionalWithoutInventingResults(t *testing.T) {
	revisionID, policyID, resultID := policyGateID(t), policyGateID(t), policyGateID(t)
	definition := versioningpolicy.Definition{Samples: 1, ThresholdID: "threshold", ThresholdOn: true, Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: "simulation", GateID: "scenario", ContractVersion: "1"}}, Scenes: []versioningpolicy.Scene{{ID: "required-scene", Required: true, Metrics: []versioningpolicy.Metric{{ID: "damage", Required: true}}}, {ID: "optional-scene", Required: false, Metrics: []versioningpolicy.Metric{{ID: "damage", Required: false}}}}}
	policy := versioningpolicy.ReleasePolicy{Definition: definition, ID: policyID, DisplayVersion: 1, CreatedAt: time.Now()}
	hash, err := policy.Hash()
	if err != nil {
		t.Fatal(err)
	}
	policy.CanonicalHash = hash
	candidate := versioningrevision.CandidateContext{RevisionID: revisionID, ConfigHash: strings.Repeat("a", 64), ManifestHash: strings.Repeat("b", 64), PolicyID: policyID}
	context := EvaluationContext{Candidate: candidate, PolicyHash: hash, SceneID: "required-scene", MetricID: "damage", ThresholdID: "threshold", ImplementationVersions: map[string]string{"simulation": "sim-v1"}}
	result := Result{ID: resultID, Descriptor: Descriptor{CapabilityID: "simulation", GateID: "scenario", ContractVersion: "1", ImplementationVersion: "sim-v1", RequiredInputs: []string{"revision_id"}, SupportedStates: []ResultState{Pass, Block, Unavailable, Stale}}, State: Pass, ResultHash: strings.Repeat("c", 64), Context: context}
	optional := result
	optional.ID = policyGateID(t)
	optional.Context = cloneContext(context)
	optional.Context.SceneID, optional.Context.MetricID = "optional-scene", "damage"
	optional.ResultHash = strings.Repeat("d", 64)
	assessment := AssessPolicy(candidate, policy, []Result{result, optional})
	if assessment.State != Pass || len(assessment.Findings) != 3 {
		t.Fatalf("complete pass=%#v", assessment)
	}
	assessment = AssessPolicy(candidate, policy, []Result{result})
	if assessment.State != Warning || len(assessment.Findings) != 3 || assessment.Findings[2].State != Warning || assessment.Findings[2].Required {
		t.Fatalf("optional absence=%#v", assessment)
	}
	assessment = AssessPolicy(candidate, policy, nil)
	if assessment.State != Block || assessment.Findings[0].State != Block || assessment.Findings[1].State != Block || assessment.Findings[2].State != Warning {
		t.Fatalf("required absence=%#v", assessment)
	}
	result.State = Unavailable
	assessment = AssessPolicy(candidate, policy, []Result{result})
	if assessment.State != Block || assessment.Findings[0].State != Block {
		t.Fatalf("required unavailable=%#v", assessment)
	}
}

func policyGateID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
