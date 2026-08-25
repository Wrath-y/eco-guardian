package acceptability

import (
	"reflect"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	simulationcontract "github.com/zouyi/eco-guardian/internal/simulation/contract"
	simulationgate "github.com/zouyi/eco-guardian/internal/simulation/gate"
	"github.com/zouyi/eco-guardian/internal/simulation/metric"
	corevalidation "github.com/zouyi/eco-guardian/internal/validation"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func TestAdvisoryPipelineGoldenIsStableAcrossOrderAndWorkerHints(t *testing.T) {
	input, proposal := acceptabilityFixture(t, false)
	validation := validationFixture(t, proposal)
	firstSimulation := simulationFixtureWithOrder(t, proposal, metric.Available, 1, false)
	firstRisk := riskFixture(t, proposal, firstSimulation, false)
	first, err := EvaluateV1(Request{Input: input, Proposal: proposal, Validation: &validation, Simulation: &firstSimulation, Risk: &firstRisk})
	if err != nil {
		t.Fatal(err)
	}

	reversedInput := input
	// Canonical input treats RequiredVersions as a semantic set.
	reversedInput.RequiredVersions = append([]aicontract.VersionIdentity(nil), input.RequiredVersions...)
	for left, right := 0, len(reversedInput.RequiredVersions)-1; left < right; left, right = left+1, right-1 {
		reversedInput.RequiredVersions[left], reversedInput.RequiredVersions[right] = reversedInput.RequiredVersions[right], reversedInput.RequiredVersions[left]
	}
	secondSimulation := simulationFixtureWithOrder(t, proposal, metric.Available, 8, true)
	secondRisk := riskFixture(t, proposal, secondSimulation, false)
	second, err := EvaluateV1(Request{Input: reversedInput, Proposal: proposal, Validation: &validation, Simulation: &secondSimulation, Risk: &secondRisk})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Acceptable || !second.Acceptable || first.Hash != second.Hash || first.InputHash != second.InputHash || firstSimulation.Hash != secondSimulation.Hash || firstRisk.Hash != secondRisk.Hash {
		t.Fatalf("first=%#v second=%#v", first, second)
	}

	registry, _ := formula.V1Registry()
	expectedValidation, _ := corevalidation.V1VersionManifest(registry)
	if validation.Versions != expectedValidation || !reflect.DeepEqual(firstSimulation.Scenes[0].Preview.Fingerprint.Simulation, simulationcontract.V1Descriptors()) {
		t.Fatalf("validation=%#v simulation=%#v", validation.Versions, firstSimulation.Scenes[0].Preview.Fingerprint.Simulation)
	}
	registeredRisk := map[string]struct{}{}
	for _, manifest := range riskcontract.V1Manifests() {
		registeredRisk[string(manifest.Kind)+"\x00"+manifest.ID+"\x00"+manifest.Version+"\x00"+manifest.SourceHash] = struct{}{}
	}
	for _, manifest := range firstRisk.Risk.RegistryManifests {
		key := string(manifest.Kind) + "\x00" + manifest.ID + "\x00" + manifest.Version + "\x00" + manifest.SourceHash
		if _, found := registeredRisk[key]; !found {
			t.Fatalf("unregistered risk manifest %#v", manifest)
		}
	}

	const validationGolden = "db74ff30b6254a94b96828a57d4346d1fa95aee985e93cf31a93728819d8be9b"
	const simulationGolden = "e8d99d80990e17756a5e6427cdf3e4929024791551d2385b742f5f30d27e6148"
	const riskGolden = "ed03cad15565ec9caac0730897b76a3cb50ae770a8e59780592d62aaa9929e78"
	const decisionGolden = "ae56bfb49532ad909efb819e9b075872ad4ae3f4e15797857d5e6206fb330a3e"
	if validation.Hash != validationGolden || firstSimulation.Hash != simulationGolden || firstRisk.Hash != riskGolden || first.Hash != decisionGolden {
		t.Fatalf("goldens validation=%s simulation=%s risk=%s decision=%s", validation.Hash, firstSimulation.Hash, firstRisk.Hash, first.Hash)
	}
}

func TestAdvisoryPreviewHasNoFormalIdentityAndCannotSatisfyReleaseGate(t *testing.T) {
	_, proposal := acceptabilityFixture(t, false)
	simulation := simulationFixture(t, proposal, metric.Available)
	risk := riskFixture(t, proposal, simulation, false)
	if _, found := reflect.TypeOf(simulation.Scenes[0].Preview).FieldByName("ID"); found {
		t.Fatal("advisory simulation unexpectedly exposes a formal run ID")
	}
	if _, found := reflect.TypeOf(risk.Risk).FieldByName("ID"); found {
		t.Fatal("advisory risk unexpectedly exposes a formal report ID")
	}

	policyID := domain.ID("018f9e40-0000-7000-8000-000000000240")
	candidate := versioningrevision.CandidateContext{RevisionID: domain.ID(proposal.Base.ConfigRevisionID), ConfigHash: string(proposal.Base.ConfigHash), ManifestHash: string(proposal.Base.VersionManifestHash), PolicyID: policyID}
	policy := versioningpolicy.ReleasePolicy{
		Definition: versioningpolicy.Definition{Samples: 1, ThresholdID: "threshold", ThresholdOn: true, Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: simulationgate.SimulationCapabilityID, GateID: simulationgate.SimulationGateID, ContractVersion: simulationgate.SimulationGateContract}}, Scenes: []versioningpolicy.Scene{{ID: "scene", Version: "v1", Required: true, Metrics: []versioningpolicy.Metric{{ID: "metric-dps", Required: true}}}}},
		ID:         policyID, DisplayVersion: 1, CanonicalHash: string(proposal.Base.ConfigHash), CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	preview := simulation.Scenes[0].Preview
	// Direct projection deliberately has no formal run ID. Even with all
	// advisory hashes copied, #7's exact evidence gate cannot consume it.
	projected := simulationgate.RunEvidence{RevisionID: candidate.RevisionID, Input: preview.Input, InputHash: preview.InputHash, FingerprintHash: preview.FingerprintHash, ResultHash: preview.ResultHash, Reproducible: true, Metrics: []simulationgate.MetricEvidence{{ID: "metric-dps", Version: "v1", Status: "available"}}}
	results := simulationgate.EvaluatePolicy(candidate, policy, "simulation-v1", map[string]string{"numeric-policy": formula.NumericPolicyV1.Version}, []simulationgate.RunEvidence{projected})
	if len(results) != 1 || results[0].State != versioninggate.Unavailable || !results[0].Valid() {
		t.Fatalf("advisory preview entered formal Gate: %#v", results)
	}
}
