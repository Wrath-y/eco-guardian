package orchestration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipatch "github.com/zouyi/eco-guardian/internal/ai/patch"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestBuildCandidateRetainsOnlyRedactedRawAuditAndCanonicalProposal(t *testing.T) {
	scope := candidateScope(t)
	response := candidateResponse(t, scope, "Evidence-backed rationale with super-secret")
	candidate, err := BuildCandidate(response, scope, aiaudit.NewRedactor([]byte("super-secret")))
	if err != nil {
		t.Fatal(err)
	}
	if !candidate.Patch.Valid() || !candidate.Diff.Valid() || !candidate.DiffHash.Valid() || candidate.Patch.Rationale != "Evidence-backed rationale with [REDACTED]" {
		t.Fatalf("candidate=%#v", candidate)
	}
	for name, body := range map[string][]byte{
		"patch": candidate.PatchCanonical, "diff": candidate.DiffCanonical, "raw audit": candidate.RawAudit.StoredBody,
	} {
		if bytes.Contains(body, []byte("super-secret")) {
			t.Fatalf("%s retained secret: %s", name, body)
		}
	}
	if candidate.RawAudit.OriginalBodyHash != response.BodyHash || candidate.RawAudit.StoredBodyHash == response.BodyHash {
		t.Fatalf("raw audit=%#v", candidate.RawAudit)
	}
	if len(candidate.Diff.Changes) != 1 || string(candidate.Diff.Changes[0].Original) != "\"500\"" || string(candidate.Diff.Changes[0].Canonical) != "\"1000\"" {
		t.Fatalf("diff=%#v", candidate.Diff)
	}
	response.Body[0] = '['
	if candidate.PatchCanonical[0] != '{' || candidate.RawAudit.StoredBody[0] != '{' {
		t.Fatal("candidate retained mutable Provider body storage")
	}
}

func TestBuildCandidateRejectsHashDriftAndRawMembersHiddenByRedaction(t *testing.T) {
	scope := candidateScope(t)
	response := candidateResponse(t, scope, "Safe rationale")
	response.BodyHash = aicontract.Hash(strings.Repeat("f", 64))
	if _, err := BuildCandidate(response, scope, aiaudit.NewRedactor()); !errors.Is(err, ErrCandidateInvalid) {
		t.Fatalf("hash drift err=%v", err)
	}

	response = candidateResponse(t, scope, "Safe rationale")
	var body map[string]any
	if json.Unmarshal(response.Body, &body) != nil {
		t.Fatal("invalid fixture")
	}
	body["analysis"] = "this field would be removed by audit redaction"
	response.Body, _ = json.Marshal(body)
	sum := sha256.Sum256(response.Body)
	response.BodyHash = aicontract.Hash(hex.EncodeToString(sum[:]))
	if _, err := BuildCandidate(response, scope, aiaudit.NewRedactor()); !errors.Is(err, ErrCandidateInvalid) {
		t.Fatalf("unknown raw member err=%v", err)
	}
}

func candidateScope(t *testing.T) aipatch.DecodeContext {
	t.Helper()
	hash := aicontract.Hash(strings.Repeat("a", 64))
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000201")
	revisionID := aicontract.RevisionID("018f9e40-0000-7000-8000-000000000202")
	entityID := aicontract.EntityID("018f9e40-0000-7000-8000-000000000203")
	fixture := aicontract.V1Fixture()
	input := aicontract.AIDesignInputV1{
		Schema: fixture.PatchSchema.Identity,
		Base: aicontract.FrozenBaseIdentity{
			ProjectID: projectID, ConfigRevisionID: revisionID, ConfigHash: hash, VersionManifestHash: hash,
			MaterializationHash: hash, GraphNamespace: string(projectID), GraphSnapshot: string(revisionID), GraphContentHash: hash,
		},
		Baseline: aicontract.BaselineIdentity{Kind: aicontract.BaselineNone},
		Goals:    []aicontract.Goal{{ID: "balance", Description: "Tune cooldown."}},
		AllowedTargets: []aicontract.AllowedTarget{{
			EntityID: entityID, Kind: "skill", ExpectedEntityVersion: 2,
			Paths: []aicontract.AllowedPath{{Path: "/payload/cooldown", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}},
		}},
		Scenes: []string{"single-target-30s"}, Budget: aicontract.Budget{Policy: fixture.Budget.Identity, BudgetLimits: fixture.Budget.Limits},
		RequiredVersions: []aicontract.VersionIdentity{{ID: "validation", Version: "v1", Hash: hash}},
	}
	entity := domain.Entity{
		ID: domain.ID(entityID), Kind: domain.KindSkill, EntityVersion: 2,
		Payload: map[string]json.RawMessage{
			"costs": json.RawMessage("[]"), "cooldown": json.RawMessage("\"500\""),
			"target_selector": json.RawMessage("{\"type\":\"primary_target\"}"), "effect_ids": json.RawMessage("[]"), "rule_blocks": json.RawMessage("[]"),
		},
	}
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := aipatch.BuildDecodeContext(
		"018f9e40-0000-7000-8000-000000000205", input,
		aicontract.VersionIdentity{ID: "retrieval-evidence", Version: "v1", Hash: hash},
		[]aicontract.EvidenceID{"evidence-a"}, registry, []domain.Entity{entity},
	)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func candidateResponse(t *testing.T, scope aipatch.DecodeContext, rationale string) aiprovider.StructuredResponse {
	t.Helper()
	target := scope.Input.AllowedTargets[0]
	body := map[string]any{
		"id": scope.PatchID, "schema": scope.Input.Schema, "base": scope.Input.Base,
		"evidence_manifest_identity": scope.EvidenceManifestIdentity,
		"targets": []any{map[string]any{
			"entity_id": target.EntityID, "kind": target.Kind, "expected_entity_version": target.ExpectedEntityVersion,
			"operations": []any{map[string]any{
				"ordinal": 1, "kind": "replace", "path": "/payload/cooldown", "value": "1000", "evidence": []string{"evidence-a"},
			}},
		}},
		"rationale": rationale, "assumptions": []string{"The fixed scene remains representative."},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return aiprovider.StructuredResponse{
		Schema: scope.Input.Schema, Body: raw, BodyHash: aicontract.Hash(hex.EncodeToString(sum[:])),
	}
}
