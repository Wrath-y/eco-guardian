package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipreview "github.com/zouyi/eco-guardian/internal/ai/preview"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	corevalidation "github.com/zouyi/eco-guardian/internal/validation"
)

func TestEvaluateV1CarriesFormalIssuesAndMakesBlocksNonOverridable(t *testing.T) {
	proposal := sealedProposal(t, previewValidationEntities())
	schemas, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := formula.V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	versions, err := corevalidation.V1VersionManifest(registry)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := corevalidation.AnalyzeV1(context.Background(), schemas, registry, versions, proposal.Entities, corevalidation.ScopeFull)
	if err != nil {
		t.Fatal(err)
	}
	result, err := EvaluateV1(context.Background(), proposal, schemas, registry, versions)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() || !result.Advisory || result.MaterializationHash != string(proposal.Hash) || len(result.Issues) != len(expected) || len(expected) != 1 {
		t.Fatalf("result=%#v expected=%#v", result, expected)
	}
	if !reflect.DeepEqual(result.Issues[0].Issue, expected[0]) || result.Issues[0].OverridePolicy != PolicyNonOverridable {
		t.Fatalf("issue=%#v expected=%#v", result.Issues[0], expected[0])
	}
	// A model-authored claim cannot lower a deterministic BLOCK or replace its
	// evidence while retaining a valid sealed result.
	tampered := result
	tampered.Issues = append([]IssueV1(nil), result.Issues...)
	tampered.Issues[0].Issue.Severity = corevalidation.SeverityWarning
	tampered.Issues[0].OverridePolicy = PolicyAdvisory
	if tampered.Valid() {
		t.Fatal("model severity override retained a valid result")
	}
	tampered = result
	tampered.Issues = append([]IssueV1(nil), result.Issues...)
	tampered.Issues[0].OverridePolicy = "model_override"
	if tampered.Valid() {
		t.Fatal("model override policy retained a valid result")
	}
}

func TestEvaluateV1RejectsMaterializationAndVersionDrift(t *testing.T) {
	proposal := sealedProposal(t, previewValidationEntities())
	schemas, _ := domain.NewRegistry()
	registry, _ := formula.V1Registry()
	versions, _ := corevalidation.V1VersionManifest(registry)
	proposal.Entities[0].Name = "unsealed mutation"
	if _, err := EvaluateV1(context.Background(), proposal, schemas, registry, versions); err == nil {
		t.Fatal("mutated materialization was accepted")
	}
	proposal = sealedProposal(t, previewValidationEntities())
	versions.NumericPolicy = "model-policy"
	if _, err := EvaluateV1(context.Background(), proposal, schemas, registry, versions); err == nil {
		t.Fatal("version drift was accepted")
	}
}

func sealedProposal(t *testing.T, entities []domain.Entity) aipreview.ProposalMaterializationV1 {
	t.Helper()
	hash := aicontract.Hash(strings.Repeat("a", 64))
	base := aicontract.FrozenBaseIdentity{
		ProjectID: "018f9e40-0000-7000-8000-000000000201", ConfigRevisionID: "018f9e40-0000-7000-8000-000000000202",
		ConfigHash: hash, VersionManifestHash: hash, MaterializationHash: hash,
		GraphNamespace: "018f9e40-0000-7000-8000-000000000201", GraphSnapshot: "018f9e40-0000-7000-8000-000000000202", GraphContentHash: hash,
	}
	value := aipreview.ProposalMaterializationV1{
		Version: aipreview.ProposalMaterializationVersionV1, Base: base, PatchID: "018f9e40-0000-7000-8000-000000000205", PatchHash: hash, Entities: entities,
	}
	canonical, err := domain.CanonicalJSON(map[string]any{
		"version": value.Version, "base": value.Base, "patch_id": value.PatchID, "patch_hash": value.PatchHash, "entities": value.Entities,
	})
	if err != nil {
		t.Fatal(err)
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("eco-guardian.ai-proposal-materialization/v1"))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(canonical)
	value.Canonical = canonical
	value.Hash = aicontract.Hash(hex.EncodeToString(hasher.Sum(nil)))
	if !value.Valid() {
		t.Fatal("test proposal seal invalid")
	}
	return value
}

func previewValidationEntities() []domain.Entity {
	now := time.Unix(1_700_000_000, 0).UTC()
	return []domain.Entity{
		{ID: "018f9e40-0000-7000-8000-000000000209", Kind: domain.KindTag, Key: "tag", Name: "Tag", Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"category": json.RawMessage(`"test"`), "parent_tag_ids": json.RawMessage(`[]`)}, Extensions: map[string]json.RawMessage{}, TagIDs: []domain.ID{}, EntityVersion: 1, CreatedAt: now, UpdatedAt: now},
		{ID: "018f9e40-0000-7000-8000-000000000210", Kind: domain.KindSkill, Key: "skill", Name: "Skill", Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"costs": json.RawMessage(`[]`), "cooldown": json.RawMessage(`"1000"`), "target_selector": json.RawMessage(`{"type":"primary_target"}`), "effect_ids": json.RawMessage(`["018f9e40-0000-7000-8000-000000000221"]`), "rule_blocks": json.RawMessage(`[]`)}, Extensions: map[string]json.RawMessage{}, TagIDs: []domain.ID{}, EntityVersion: 3, CreatedAt: now, UpdatedAt: now},
	}
}
