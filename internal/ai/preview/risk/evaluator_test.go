package risk

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
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	riskpreview "github.com/zouyi/eco-guardian/internal/risk/preview"
)

type riskEvaluatorFake struct {
	request riskpreview.Request
	result  riskpreview.ResultV1
}

func (fake *riskEvaluatorFake) Evaluate(_ context.Context, request riskpreview.Request) (riskpreview.ResultV1, error) {
	fake.request = request
	registries, _ := riskcontract.V1Registries()
	result := riskpreview.ResultV1{
		SchemaVersion: riskpreview.SchemaVersionV1, Advisory: true, ProposalMaterializationHash: request.ProposalMaterializationHash,
		InputHash: strings.Repeat("b", 64), RegistryManifests: registries.Threshold.Manifests(), Metrics: []riskpreview.MetricResult{}, Structural: []riskpreview.StructuralResult{}, Acceptable: true,
	}
	result.ResultHash, _, _ = riskcontract.CanonicalHash("eco-guardian/risk-preview-result/v1", result)
	fake.result = result
	return result, nil
}

func TestEvaluateBindsPureRiskEvidenceToProposal(t *testing.T) {
	proposal := riskProposal(t)
	fake := &riskEvaluatorFake{}
	request := Request{Proposal: proposal, Risk: riskpreview.Request{SchemaVersion: riskpreview.SchemaVersionV1, ProjectID: domain.ID(proposal.Base.ProjectID), ProposalMaterializationHash: string(proposal.Hash)}}
	result, err := (Service{Evaluator: fake}).Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() || !result.Advisory || result.ProposalHash != proposal.Hash || fake.request.ProposalMaterializationHash != string(proposal.Hash) || len(result.Risk.RegistryManifests) == 0 {
		t.Fatalf("result=%#v request=%#v", result, fake.request)
	}
	tampered := result
	tampered.Risk.Acceptable = false
	if tampered.Valid() {
		t.Fatal("model-authored risk mutation retained a valid result")
	}
}

func TestEvaluateRejectsCrossProjectAndUnsealedProposal(t *testing.T) {
	proposal := riskProposal(t)
	request := Request{Proposal: proposal, Risk: riskpreview.Request{SchemaVersion: riskpreview.SchemaVersionV1, ProjectID: "018f9e40-0000-7000-8000-000000000299", ProposalMaterializationHash: string(proposal.Hash)}}
	if _, err := (Service{Evaluator: &riskEvaluatorFake{}}).Evaluate(context.Background(), request); err == nil {
		t.Fatal("cross-project request was accepted")
	}
	request.Risk.ProjectID = domain.ID(proposal.Base.ProjectID)
	request.Proposal.Canonical[0] = '['
	if _, err := (Service{Evaluator: &riskEvaluatorFake{}}).Evaluate(context.Background(), request); err == nil {
		t.Fatal("unsealed proposal was accepted")
	}
}

func riskProposal(t *testing.T) aipreview.ProposalMaterializationV1 {
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
