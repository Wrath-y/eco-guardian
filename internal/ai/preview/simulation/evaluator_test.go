package simulation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipreview "github.com/zouyi/eco-guardian/internal/ai/preview"
	"github.com/zouyi/eco-guardian/internal/domain"
	simulationcontract "github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/metric"
	simulationpreview "github.com/zouyi/eco-guardian/internal/simulation/preview"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

type simulationEvaluatorFake struct {
	requests []simulationpreview.Request
}

func (fake *simulationEvaluatorFake) Evaluate(_ context.Context, request simulationpreview.Request) (simulationpreview.ResultV1, error) {
	fake.requests = append(fake.requests, request)
	sampleCount := request.SampleCount
	if sampleCount == 0 {
		sampleCount = 1
	}
	input := simulationcontract.SimulationInputV1{
		SchemaVersion: simulationcontract.SimulationInputSchemaV1, ProjectID: request.Materialization.BaseRevision.ProjectID, RevisionID: request.Materialization.BaseRevision.ID,
		ConfigHash: request.Materialization.BaseRevision.ConfigHash, ManifestHash: request.Materialization.BaseRevision.ManifestHash, RuleMaterializationHash: request.Materialization.Hash,
		SceneID: request.SceneID, SceneVersion: request.SceneVersion, SceneBodyHash: strings.Repeat("d", 64), DurationMS: 1,
		Budgets: scenario.Budgets{MaxSamples: sampleCount, MaxEvents: 1, MaxSteps: 1, MaxRuntimeMS: 1}, SampleCount: sampleCount, Metrics: append([]simulationcontract.MetricIdentity(nil), request.Metrics...),
	}
	inputHash, _ := input.Hash()
	fingerprintHash := strings.Repeat("e", 64)
	result := metric.CanonicalResultV1{
		SchemaVersion: "v1", InputHash: inputHash, FingerprintHash: fingerprintHash,
		Metrics:  []metric.CanonicalMetric{{ID: request.Metrics[0].ID, Version: request.Metrics[0].Version, Status: metric.Available, Unit: "points", Direction: metric.HigherIsRisk, Value: "10", ConfidenceLow: "9", ConfidenceHigh: "11", SampleCount: sampleCount, Assumptions: []string{"fixed scene"}}},
		Warnings: []string{},
	}
	resultHash, _ := result.Hash()
	return simulationpreview.ResultV1{
		SchemaVersion: "v1", Advisory: true, MaterializationHash: request.Materialization.Hash, Input: input, InputHash: inputHash,
		Fingerprint:     simulationcontract.ImplementationFingerprint{RevisionManifestHash: input.ManifestHash, SceneBodyHash: input.SceneBodyHash, Revision: append([]simulationcontract.RevisionImplementation(nil), request.RevisionImplementations...), Simulation: []simulationcontract.Descriptor{simulationcontract.StableDescriptor("simulation-evaluator-adapter", "v1")}},
		FingerprintHash: fingerprintHash, Result: result, ResultHash: resultHash,
	}, nil
}

func TestEvaluateBindsProposalToFixedSceneAdvisoryResults(t *testing.T) {
	proposal := simulationProposal(t)
	fake := &simulationEvaluatorFake{}
	request := Request{
		Proposal: proposal,
		Scenes: []SceneRequest{
			{SceneID: "scene-z", SceneVersion: "v1", SampleCount: 2, Metrics: []simulationcontract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}},
			{SceneID: "scene-a", SceneVersion: "v1", SampleCount: 2, Metrics: []simulationcontract.MetricIdentity{{ID: "metric-healing", Version: "v1"}}},
		},
		RevisionImplementations: []simulationcontract.RevisionImplementation{{CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "schema-v1", State: "registered"}},
	}
	result, err := (Service{Evaluator: fake}).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() || !result.Advisory || result.ProposalHash != proposal.Hash || len(result.Scenes) != 2 || result.Scenes[0].SceneID != "scene-a" || len(fake.requests) != 2 {
		t.Fatalf("result=%#v requests=%d", result, len(fake.requests))
	}
	for _, scene := range result.Scenes {
		if scene.Preview.Input.RevisionID != simulationcontract.ID(proposal.Base.ConfigRevisionID) || scene.Preview.Input.ConfigHash != string(proposal.Base.ConfigHash) || len(scene.Preview.Result.Metrics[0].Assumptions) == 0 || len(scene.Preview.Fingerprint.Simulation) == 0 {
			t.Fatalf("scene=%#v", scene)
		}
	}
	tampered := result
	tampered.Scenes = append([]SceneResult(nil), result.Scenes...)
	tampered.Scenes[0].Preview.Result.Metrics[0].Value = "model estimate"
	if tampered.Valid() {
		t.Fatal("model-authored metric mutation retained a valid result")
	}
}

func TestEvaluateRejectsDuplicateScenesAndUnsealedProposal(t *testing.T) {
	proposal := simulationProposal(t)
	request := Request{Proposal: proposal, Scenes: []SceneRequest{{SceneID: "same", SceneVersion: "v1", Metrics: []simulationcontract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}}, {SceneID: "same", SceneVersion: "v1", Metrics: []simulationcontract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}}}, RevisionImplementations: []simulationcontract.RevisionImplementation{{CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "v1", State: "registered"}}}
	if _, err := (Service{Evaluator: &simulationEvaluatorFake{}}).Evaluate(context.Background(), request); err == nil {
		t.Fatal("duplicate scenes were accepted")
	}
	request.Scenes = request.Scenes[:1]
	request.Proposal.Entities[0].Name = "unsealed"
	if _, err := (Service{Evaluator: &simulationEvaluatorFake{}}).Evaluate(context.Background(), request); err == nil {
		t.Fatal("unsealed proposal was accepted")
	}
}

func simulationProposal(t *testing.T) aipreview.ProposalMaterializationV1 {
	t.Helper()
	hash := aicontract.Hash(strings.Repeat("a", 64))
	base := aicontract.FrozenBaseIdentity{ProjectID: "018f9e40-0000-7000-8000-000000000201", ConfigRevisionID: "018f9e40-0000-7000-8000-000000000202", ConfigHash: hash, VersionManifestHash: hash, MaterializationHash: hash, GraphNamespace: "018f9e40-0000-7000-8000-000000000201", GraphSnapshot: "018f9e40-0000-7000-8000-000000000202", GraphContentHash: hash}
	now := time.Unix(1_700_000_000, 0).UTC()
	entities := []domain.Entity{{ID: "018f9e40-0000-7000-8000-000000000210", Kind: domain.KindTag, Key: "tag", Name: "Tag", Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"category": json.RawMessage(`"test"`), "parent_tag_ids": json.RawMessage(`[]`)}, Extensions: map[string]json.RawMessage{}, TagIDs: []domain.ID{}, EntityVersion: 1, CreatedAt: now, UpdatedAt: now}}
	value := aipreview.ProposalMaterializationV1{Version: aipreview.ProposalMaterializationVersionV1, Base: base, PatchID: "018f9e40-0000-7000-8000-000000000205", PatchHash: hash, Entities: entities}
	canonical, _ := domain.CanonicalJSON(map[string]any{"version": value.Version, "base": value.Base, "patch_id": value.PatchID, "patch_hash": value.PatchHash, "entities": value.Entities})
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("eco-guardian.ai-proposal-materialization/v1"))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(canonical)
	value.Canonical, value.Hash = canonical, aicontract.Hash(hex.EncodeToString(hasher.Sum(nil)))
	return value
}
