package acceptability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipreview "github.com/zouyi/eco-guardian/internal/ai/preview"
	airisk "github.com/zouyi/eco-guardian/internal/ai/preview/risk"
	aisearch "github.com/zouyi/eco-guardian/internal/ai/preview/search"
	aisimulation "github.com/zouyi/eco-guardian/internal/ai/preview/simulation"
	aivalidation "github.com/zouyi/eco-guardian/internal/ai/preview/validation"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	riskcomparison "github.com/zouyi/eco-guardian/internal/risk/comparison"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	riskpreview "github.com/zouyi/eco-guardian/internal/risk/preview"
	simulationcontract "github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/metric"
	simulationpreview "github.com/zouyi/eco-guardian/internal/simulation/preview"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
	corevalidation "github.com/zouyi/eco-guardian/internal/validation"
)

func TestEvaluateAcceptsOnlyCompleteConsistentDeterministicEvidence(t *testing.T) {
	input, proposal := acceptabilityFixture(t, false)
	validation := validationFixture(t, proposal)
	simulation := simulationFixture(t, proposal, metric.Available)
	risk := riskFixture(t, proposal, simulation, false)
	result, err := EvaluateV1(Request{Input: input, Proposal: proposal, Validation: &validation, Simulation: &simulation, Risk: &risk})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() || !result.Acceptable || len(result.Reasons) != 0 || result.Evidence.ValidationHash == "" || result.Evidence.SimulationHash == "" || result.Evidence.RiskHash == "" {
		t.Fatalf("result=%#v", result)
	}
}

func TestEvaluateMarksMissingEvaluatorsAndInputsUnacceptable(t *testing.T) {
	input, proposal := acceptabilityFixture(t, false)
	result, err := EvaluateV1(Request{Input: input, Proposal: proposal, SearchRequired: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []ReasonCode{ValidationUnavailable, SearchUnavailable, SimulationUnavailable, RiskUnavailable} {
		if !hasReason(result, code) {
			t.Fatalf("missing %s in %#v", code, result.Reasons)
		}
	}
	if result.Acceptable || !result.Valid() {
		t.Fatalf("result=%#v", result)
	}
}

func TestEvaluatePreservesBlockUnavailableMetricAndBudgetExhaustion(t *testing.T) {
	input, proposal := acceptabilityFixture(t, true)
	validation := validationFixture(t, proposal)
	simulation := simulationFixture(t, proposal, metric.Unavailable)
	risk := riskFixture(t, proposal, simulation, true)
	search := searchFixture(t, input, proposal)
	result, err := EvaluateV1(Request{Input: input, Proposal: proposal, Validation: &validation, SearchRequired: true, Search: &search, Simulation: &simulation, Risk: &risk})
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []ReasonCode{ValidationBlocked, SearchBudgetExhausted, MetricUnavailable, RiskBlocked} {
		if !hasReason(result, code) {
			t.Fatalf("missing %s in %#v", code, result.Reasons)
		}
	}
	value := simulation.Scenes[0].Preview.Result.Metrics[0]
	if value.Status != metric.Unavailable || value.Value != "" || value.Unavailable == nil || risk.Risk.Metrics[0].Assessment.PolicyEffect == nil || *risk.Risk.Metrics[0].Assessment.PolicyEffect != riskcontract.Block || result.Acceptable {
		t.Fatalf("simulation=%#v risk=%#v result=%#v", value, risk.Risk.Metrics[0], result)
	}
}

func TestEvaluateRejectsInconsistentPreviewInsteadOfTrustingModelText(t *testing.T) {
	input, proposal := acceptabilityFixture(t, false)
	validation := validationFixture(t, proposal)
	simulation := simulationFixture(t, proposal, metric.Available)
	risk := riskFixture(t, proposal, simulation, false)
	simulation.Scenes[0].Preview.Result.Metrics[0].Value = "999"
	result, err := EvaluateV1(Request{Input: input, Proposal: proposal, Validation: &validation, Simulation: &simulation, Risk: &risk})
	if err != nil {
		t.Fatal(err)
	}
	if result.Acceptable || !hasReason(result, PreviewInconsistent) {
		t.Fatalf("result=%#v", result)
	}
}

func hasReason(result ResultV1, code ReasonCode) bool {
	for _, reason := range result.Reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}

func acceptabilityFixture(t *testing.T, blocked bool) (aicontract.AIDesignInputV1, aipreview.ProposalMaterializationV1) {
	t.Helper()
	hash := aicontract.Hash(strings.Repeat("a", 64))
	base := aicontract.FrozenBaseIdentity{ProjectID: "018f9e40-0000-7000-8000-000000000201", ConfigRevisionID: "018f9e40-0000-7000-8000-000000000202", ConfigHash: hash, VersionManifestHash: hash, MaterializationHash: hash, GraphNamespace: "018f9e40-0000-7000-8000-000000000201", GraphSnapshot: "018f9e40-0000-7000-8000-000000000202", GraphContentHash: hash}
	fixture := aicontract.V1Fixture()
	tagID := aicontract.EntityID("018f9e40-0000-7000-8000-000000000210")
	input := aicontract.AIDesignInputV1{
		Schema: fixture.PatchSchema.Identity, Base: base, Baseline: aicontract.BaselineIdentity{Kind: aicontract.BaselineNone},
		Goals:          []aicontract.Goal{{ID: "balance", Description: "Balance the fixed scene."}},
		Metrics:        []aicontract.MetricGoal{{MetricID: "metric-dps", Version: "v1", Direction: aicontract.MetricMinimize, Unit: "points"}},
		AllowedTargets: []aicontract.AllowedTarget{{EntityID: tagID, Kind: "tag", ExpectedEntityVersion: 1, Paths: []aicontract.AllowedPath{{Path: "/payload/category", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}}},
		Scenes:         []string{"scene"}, Budget: aicontract.Budget{Policy: fixture.Budget.Identity, BudgetLimits: fixture.Budget.Limits},
		RequiredVersions: []aicontract.VersionIdentity{{ID: "validation", Version: "v1", Hash: hash}, {ID: "simulation", Version: "v1", Hash: hash}, {ID: "risk", Version: "v1", Hash: hash}, {ID: "numeric-policy", Version: "v1", Hash: hash}},
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	entities := []domain.Entity{{ID: domain.ID(tagID), Kind: domain.KindTag, Key: "tag", Name: "Tag", Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"category": json.RawMessage(`"test"`), "parent_tag_ids": json.RawMessage(`[]`)}, Extensions: map[string]json.RawMessage{}, TagIDs: []domain.ID{}, EntityVersion: 1, CreatedAt: now, UpdatedAt: now}}
	if blocked {
		entities = append(entities, domain.Entity{ID: "018f9e40-0000-7000-8000-000000000211", Kind: domain.KindSkill, Key: "skill", Name: "Skill", Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"costs": json.RawMessage(`[]`), "cooldown": json.RawMessage(`"1"`), "target_selector": json.RawMessage(`{"type":"primary_target"}`), "effect_ids": json.RawMessage(`["018f9e40-0000-7000-8000-000000000299"]`), "rule_blocks": json.RawMessage(`[]`)}, Extensions: map[string]json.RawMessage{}, TagIDs: []domain.ID{}, EntityVersion: 1, CreatedAt: now, UpdatedAt: now})
	}
	proposal := aipreview.ProposalMaterializationV1{Version: aipreview.ProposalMaterializationVersionV1, Base: base, PatchID: "018f9e40-0000-7000-8000-000000000205", PatchHash: hash, Entities: entities}
	canonical, _ := domain.CanonicalJSON(map[string]any{"version": proposal.Version, "base": proposal.Base, "patch_id": proposal.PatchID, "patch_hash": proposal.PatchHash, "entities": proposal.Entities})
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("eco-guardian.ai-proposal-materialization/v1"))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(canonical)
	proposal.Canonical, proposal.Hash = canonical, aicontract.Hash(hex.EncodeToString(hasher.Sum(nil)))
	return input, proposal
}

func validationFixture(t *testing.T, proposal aipreview.ProposalMaterializationV1) aivalidation.ResultV1 {
	t.Helper()
	schemas, _ := domain.NewRegistry()
	registry, _ := formula.V1Registry()
	versions, _ := corevalidation.V1VersionManifest(registry)
	result, err := aivalidation.EvaluateV1(context.Background(), proposal, schemas, registry, versions)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type acceptabilitySimulationEvaluator struct {
	status  metric.Status
	workers int // scheduling is deliberately outside semantic preview identity
}

func (fake acceptabilitySimulationEvaluator) Evaluate(_ context.Context, request simulationpreview.Request) (simulationpreview.ResultV1, error) {
	revisionImplementations := append([]simulationcontract.RevisionImplementation(nil), request.RevisionImplementations...)
	sort.Slice(revisionImplementations, func(i, j int) bool {
		return revisionImplementations[i].CapabilityID < revisionImplementations[j].CapabilityID
	})
	input := simulationcontract.SimulationInputV1{SchemaVersion: "v1", ProjectID: request.Materialization.BaseRevision.ProjectID, RevisionID: request.Materialization.BaseRevision.ID, ConfigHash: request.Materialization.BaseRevision.ConfigHash, ManifestHash: request.Materialization.BaseRevision.ManifestHash, RuleMaterializationHash: request.Materialization.Hash, SceneID: request.SceneID, SceneVersion: request.SceneVersion, SceneBodyHash: strings.Repeat("b", 64), DurationMS: 1, Budgets: scenario.Budgets{MaxSamples: 1, MaxEvents: 1, MaxSteps: 1, MaxRuntimeMS: 1}, SampleCount: 1, Metrics: append([]simulationcontract.MetricIdentity(nil), request.Metrics...)}
	inputHash, _ := input.Hash()
	fingerprintHash := strings.Repeat("c", 64)
	value := metric.CanonicalMetric{ID: "metric-dps", Version: "v1", Status: fake.status, Unit: "points", Direction: metric.HigherIsRisk, SampleCount: 1, Assumptions: []string{"fixed scene"}}
	if fake.status == metric.Available {
		value.Value, value.ConfidenceLow, value.ConfidenceHigh = "0", "0", "0"
	} else {
		value.Unavailable = &metric.UnavailableReason{Code: "MISSING_OBSERVATION", Missing: []string{"damage"}, Message: "required observation unavailable"}
	}
	canonical := metric.CanonicalResultV1{SchemaVersion: "v1", InputHash: inputHash, FingerprintHash: fingerprintHash, Metrics: []metric.CanonicalMetric{value}, Warnings: []string{}}
	resultHash, _ := canonical.Hash()
	return simulationpreview.ResultV1{SchemaVersion: "v1", Advisory: true, MaterializationHash: request.Materialization.Hash, Input: input, InputHash: inputHash, Fingerprint: simulationcontract.ImplementationFingerprint{RevisionManifestHash: input.ManifestHash, SceneBodyHash: input.SceneBodyHash, Revision: revisionImplementations, Simulation: simulationcontract.V1Descriptors()}, FingerprintHash: fingerprintHash, Result: canonical, ResultHash: resultHash}, nil
}

func simulationFixture(t *testing.T, proposal aipreview.ProposalMaterializationV1, status metric.Status) aisimulation.ResultV1 {
	return simulationFixtureWithOrder(t, proposal, status, 1, false)
}

func simulationFixtureWithOrder(t *testing.T, proposal aipreview.ProposalMaterializationV1, status metric.Status, workers int, reverse bool) aisimulation.ResultV1 {
	t.Helper()
	implementations := []simulationcontract.RevisionImplementation{{CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "schema-v1", State: "registered"}, {CapabilityID: "dsl", ContractVersion: "v1", ImplementationVersion: formula.DSLVersion, State: "registered"}, {CapabilityID: "validator-registry", ContractVersion: "v1", ImplementationVersion: "registry-v1", State: "registered"}, {CapabilityID: "numeric-policy", ContractVersion: "v1", ImplementationVersion: formula.NumericPolicyV1.Version, State: "registered"}}
	if reverse {
		for left, right := 0, len(implementations)-1; left < right; left, right = left+1, right-1 {
			implementations[left], implementations[right] = implementations[right], implementations[left]
		}
	}
	result, err := (aisimulation.Service{Evaluator: acceptabilitySimulationEvaluator{status: status, workers: workers}}).Evaluate(context.Background(), aisimulation.Request{Proposal: proposal, Scenes: []aisimulation.SceneRequest{{SceneID: "scene", SceneVersion: "v1", SampleCount: 1, Metrics: []simulationcontract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}}}, RevisionImplementations: implementations})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type acceptabilityRiskEvaluator struct{ block bool }

func (fake acceptabilityRiskEvaluator) Evaluate(_ context.Context, request riskpreview.Request) (riskpreview.ResultV1, error) {
	comparison := request.Comparisons[0]
	severity := riskcontract.Warning
	if fake.block {
		severity = riskcontract.Block
	}
	registries, _ := riskcontract.V1Registries()
	result := riskpreview.ResultV1{SchemaVersion: "v1", Advisory: true, ProposalMaterializationHash: request.ProposalMaterializationHash, InputHash: strings.Repeat("d", 64), RegistryManifests: registries.Threshold.Manifests(), Metrics: []riskpreview.MetricResult{{ID: comparison.ID, Role: comparison.Role, SceneID: comparison.SceneID, SceneVersion: comparison.SceneVersion, Candidate: comparison.Candidate, Baseline: comparison.Baseline, CandidateEvidence: comparison.CandidateEvidence, BaselineEvidence: comparison.BaselineEvidence, Assessment: riskcomparison.Assessment{Status: riskcontract.Comparable, Severity: &severity, PolicyEffect: &severity, Override: riskcontract.NonOverridable}, EvidenceHash: strings.Repeat("e", 64)}}, Structural: []riskpreview.StructuralResult{}, Acceptable: !fake.block}
	result.ResultHash, _, _ = riskcontract.CanonicalHash("eco-guardian/risk-preview-result/v1", result)
	return result, nil
}

func riskFixture(t *testing.T, proposal aipreview.ProposalMaterializationV1, simulation aisimulation.ResultV1, block bool) airisk.ResultV1 {
	t.Helper()
	scene := simulation.Scenes[0].Preview
	value := "0"
	metricStatus := riskcontract.MetricAvailable
	var unavailable *riskcontract.UnavailableReason
	if scene.Result.Metrics[0].Status == metric.Unavailable {
		metricStatus = riskcontract.MetricUnavailable
		value = ""
		unavailable = &riskcontract.UnavailableReason{Code: "MISSING_OBSERVATION", Missing: []string{"damage"}, Message: "required observation unavailable"}
	}
	metricValue := &value
	low, high := metricValue, metricValue
	if metricStatus == riskcontract.MetricUnavailable {
		metricValue, low, high = nil, nil, nil
	}
	candidate := riskcontract.MetricEvidence{MetricID: "metric-dps", MetricVersion: "v1", Status: metricStatus, Unit: "points", Direction: riskcontract.HigherIsRisk, Value: metricValue, ConfidenceLow: low, ConfidenceHigh: high, SampleCount: 1, Assumptions: []string{"fixed scene"}, Unavailable: unavailable, CanonicalHash: strings.Repeat("f", 64)}
	baselineValue := "0"
	baseline := riskcontract.MetricEvidence{MetricID: "metric-dps", MetricVersion: "v1", Status: riskcontract.MetricAvailable, Unit: "points", Direction: riskcontract.HigherIsRisk, Value: &baselineValue, ConfidenceLow: &baselineValue, ConfidenceHigh: &baselineValue, SampleCount: 1, Assumptions: []string{"baseline"}, CanonicalHash: strings.Repeat("1", 64)}
	request := riskpreview.Request{SchemaVersion: "v1", ProjectID: domain.ID(proposal.Base.ProjectID), ProposalMaterializationHash: string(proposal.Hash), Comparisons: []riskpreview.MetricComparison{{ID: "comparison", Role: riskcontract.Required, SceneID: "scene", SceneVersion: "v1", Candidate: candidate, Baseline: baseline, CandidateEvidence: riskpreview.EvidenceRef{Kind: riskpreview.AdvisorySimulation, ID: "scene-preview", InputHash: scene.InputHash, FingerprintHash: scene.FingerprintHash, ResultHash: scene.ResultHash}, BaselineEvidence: riskpreview.EvidenceRef{Kind: riskpreview.FormalSimulation, ID: "baseline", InputHash: strings.Repeat("2", 64), FingerprintHash: strings.Repeat("3", 64), ResultHash: strings.Repeat("4", 64)}}}}
	result, err := (airisk.Service{Evaluator: acceptabilityRiskEvaluator{block: block}}).Evaluate(context.Background(), airisk.Request{Proposal: proposal, Risk: request})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type acceptabilitySearchEvaluator struct{ descriptor aisearch.EvaluatorDescriptor }

func (fake acceptabilitySearchEvaluator) Descriptor() aisearch.EvaluatorDescriptor {
	return fake.descriptor
}
func (fake acceptabilitySearchEvaluator) EvaluateCandidate(context.Context, aisearch.Candidate) (aisearch.Evaluation, error) {
	return aisearch.Evaluation{Status: aisearch.EvaluationPassed, ValidationResultHash: strings.Repeat("5", 64), SimulationResultHash: strings.Repeat("6", 64)}, nil
}

func searchFixture(t *testing.T, input aicontract.AIDesignInputV1, proposal aipreview.ProposalMaterializationV1) aisearch.ResultV1 {
	t.Helper()
	hash := aicontract.Hash(strings.Repeat("7", 64))
	descriptor := aisearch.EvaluatorDescriptor{Identity: aicontract.VersionIdentity{ID: "candidate-evaluator", Version: "v1", Hash: hash}, Validation: aicontract.VersionIdentity{ID: "validation", Version: "v1", Hash: hash}, Simulation: aicontract.VersionIdentity{ID: "simulation", Version: "v1", Hash: hash}}
	result, err := (aisearch.Service{Evaluator: acceptabilitySearchEvaluator{descriptor: descriptor}, Registered: descriptor}).Search(context.Background(), aisearch.Request{Base: proposal.Base, MaterializationHash: proposal.Hash, AllowedTargets: input.AllowedTargets, Dimensions: []aisearch.Dimension{{EntityID: input.AllowedTargets[0].EntityID, Path: input.AllowedTargets[0].Paths[0].Path, Kind: aisearch.Duration, Values: []string{"1", "2"}}}, Budget: aisearch.Budget{MaxCandidates: 1, MaxDurationMillis: 1_000}})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
