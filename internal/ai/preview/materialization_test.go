package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipatch "github.com/zouyi/eco-guardian/internal/ai/patch"
	"github.com/zouyi/eco-guardian/internal/domain"
)

type proposalReaderFake struct {
	snapshot ProposalBaseSnapshot
	err      error
	calls    int
}

func (r *proposalReaderFake) ReadProposalBase(_ context.Context, _ aicontract.FrozenBaseIdentity) (ProposalBaseSnapshot, error) {
	r.calls++
	return r.snapshot, r.err
}

func TestBuildProposalMaterializationAppliesPatchOnlyToIsolatedClone(t *testing.T) {
	value, diff, base, source := proposalFixture(t)
	reader := &proposalReaderFake{snapshot: ProposalBaseSnapshot{Base: base, Entities: source}}
	result, err := BuildProposalMaterializationV1(context.Background(), reader, value, diff)
	if err != nil {
		t.Fatal(err)
	}
	if reader.calls != 1 || result.Version != ProposalMaterializationVersionV1 || result.Base != base || result.PatchID != value.ID || result.PatchHash != value.Hash || !result.Valid() || len(result.Canonical) == 0 {
		t.Fatalf("result=%#v calls=%d", result, reader.calls)
	}
	var proposed domain.Entity
	for _, entity := range result.Entities {
		if aicontract.EntityID(entity.ID) == value.Targets[0].EntityID {
			proposed = entity
		}
	}
	if proposed.EntityVersion != 3 || string(proposed.Payload["cooldown"]) != "\"1000\"" || string(proposed.Payload["effect_ids"]) != "[\"018f9e40-0000-7000-8000-000000000221\"]" {
		t.Fatalf("proposed=%#v", proposed)
	}
	if source[1].EntityVersion != 2 || string(source[1].Payload["cooldown"]) != "\"500\"" || string(source[1].Payload["effect_ids"]) != "[]" {
		t.Fatalf("source was mutated=%#v", source[1])
	}
	if !bytes.Contains(result.Canonical, []byte("\"version\":\"proposal-materialization-v1\"")) || !bytes.Contains(result.Canonical, []byte("\"patch_hash\"")) {
		t.Fatalf("canonical=%s", result.Canonical)
	}
	result.Entities[0].Name = "mutated"
	if result.Valid() {
		t.Fatal("mutated sealed materialization remained valid")
	}
}

func TestProposalMaterializationIsStableUnderSourceOrder(t *testing.T) {
	value, diff, base, source := proposalFixture(t)
	first, err := BuildProposalMaterializationV1(context.Background(), &proposalReaderFake{snapshot: ProposalBaseSnapshot{Base: base, Entities: source}}, value, diff)
	if err != nil {
		t.Fatal(err)
	}
	source[0], source[1] = source[1], source[0]
	second, err := BuildProposalMaterializationV1(context.Background(), &proposalReaderFake{snapshot: ProposalBaseSnapshot{Base: base, Entities: source}}, value, diff)
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash != second.Hash || !bytes.Equal(first.Canonical, second.Canonical) || first.Entities[0].ID >= first.Entities[1].ID || second.Entities[0].ID >= second.Entities[1].ID {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
}

func TestProposalMaterializationRejectsBaseAndDiffDriftBeforeExposure(t *testing.T) {
	value, diff, base, source := proposalFixture(t)
	mismatch := base
	mismatch.GraphContentHash = aicontract.Hash(strings.Repeat("f", 64))
	reader := &proposalReaderFake{snapshot: ProposalBaseSnapshot{Base: mismatch, Entities: source}}
	if _, err := BuildProposalMaterializationV1(context.Background(), reader, value, diff); !errors.Is(err, ErrProposalBaseMismatch) {
		t.Fatalf("base mismatch err=%v", err)
	}

	tampered := diff
	tampered.Canonical = append([]byte(nil), diff.Canonical...)
	tampered.Canonical[0] = '['
	reader = &proposalReaderFake{snapshot: ProposalBaseSnapshot{Base: base, Entities: source}}
	if _, err := BuildProposalMaterializationV1(context.Background(), reader, value, tampered); !errors.Is(err, ErrProposalMaterializationInvalid) || reader.calls != 0 {
		t.Fatalf("diff drift err=%v calls=%d", err, reader.calls)
	}

	source[1].Payload["cooldown"] = json.RawMessage("\"600\"")
	reader = &proposalReaderFake{snapshot: ProposalBaseSnapshot{Base: base, Entities: source}}
	if _, err := BuildProposalMaterializationV1(context.Background(), reader, value, diff); !errors.Is(err, ErrProposalBaseMismatch) {
		t.Fatalf("original drift err=%v", err)
	}
}

func proposalFixture(t *testing.T) (aicontract.DraftPatchV1, aipatch.DiffResult, aicontract.FrozenBaseIdentity, []domain.Entity) {
	t.Helper()
	hash := aicontract.Hash(strings.Repeat("a", 64))
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000201")
	revisionID := aicontract.RevisionID("018f9e40-0000-7000-8000-000000000202")
	skillID := aicontract.EntityID("018f9e40-0000-7000-8000-000000000210")
	fixture := aicontract.V1Fixture()
	base := aicontract.FrozenBaseIdentity{
		ProjectID: projectID, ConfigRevisionID: revisionID, ConfigHash: hash, VersionManifestHash: hash,
		MaterializationHash: hash, GraphNamespace: string(projectID), GraphSnapshot: string(revisionID), GraphContentHash: hash,
	}
	input := aicontract.AIDesignInputV1{
		Schema: fixture.PatchSchema.Identity, Base: base, Baseline: aicontract.BaselineIdentity{Kind: aicontract.BaselineNone},
		Goals: []aicontract.Goal{{ID: "balance", Description: "Tune cooldown."}},
		AllowedTargets: []aicontract.AllowedTarget{{
			EntityID: skillID, Kind: "skill", ExpectedEntityVersion: 2,
			Paths: []aicontract.AllowedPath{
				{Path: "/payload/cooldown", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}},
				{Path: "/payload/effect_ids", Operations: []aicontract.PatchOperationKind{aicontract.OperationAdd}},
			},
		}},
		Scenes: []string{"single-target-30s"}, Budget: aicontract.Budget{Policy: fixture.Budget.Identity, BudgetLimits: fixture.Budget.Limits},
		RequiredVersions: []aicontract.VersionIdentity{{ID: "validation", Version: "v1", Hash: hash}},
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	tag := domain.Entity{
		ID: "018f9e40-0000-7000-8000-000000000209", Kind: domain.KindTag, Key: "tag", Name: "Tag", Status: domain.StatusActive,
		SchemaVersion: 1, Payload: map[string]json.RawMessage{"category": json.RawMessage("\"test\""), "parent_tag_ids": json.RawMessage("[]")},
		Extensions: map[string]json.RawMessage{}, TagIDs: []domain.ID{}, EntityVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	skill := domain.Entity{
		ID: domain.ID(skillID), Kind: domain.KindSkill, Key: "skill", Name: "Skill", Status: domain.StatusActive,
		SchemaVersion: 1, Payload: map[string]json.RawMessage{
			"costs": json.RawMessage("[]"), "cooldown": json.RawMessage("\"500\""),
			"target_selector": json.RawMessage("{\"type\":\"primary_target\"}"), "effect_ids": json.RawMessage("[]"), "rule_blocks": json.RawMessage("[]"),
		},
		Extensions: map[string]json.RawMessage{}, TagIDs: []domain.ID{}, EntityVersion: 2, CreatedAt: now, UpdatedAt: now,
	}
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := aipatch.BuildDecodeContext(
		"018f9e40-0000-7000-8000-000000000205", input,
		aicontract.VersionIdentity{ID: "retrieval-evidence", Version: "v1", Hash: hash},
		[]aicontract.EvidenceID{"evidence-a"}, registry, []domain.Entity{skill},
	)
	if err != nil {
		t.Fatal(err)
	}
	wire := map[string]any{
		"id": scope.PatchID, "schema": input.Schema, "base": base, "evidence_manifest_identity": scope.EvidenceManifestIdentity,
		"targets": []any{map[string]any{
			"entity_id": skillID, "kind": "skill", "expected_entity_version": 2,
			"operations": []any{
				map[string]any{"ordinal": 1, "kind": "replace", "path": "/payload/cooldown", "value": "1000", "evidence": []string{"evidence-a"}},
				map[string]any{"ordinal": 2, "kind": "add", "path": "/payload/effect_ids", "value": "018f9e40-0000-7000-8000-000000000221", "evidence": []string{"evidence-a"}},
			},
		}},
		"rationale": "Tune the frozen proposal.", "assumptions": []string{"The scene is representative."},
	}
	raw, _ := json.Marshal(wire)
	value, _, err := aipatch.DecodeV1(raw, scope)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := aipatch.BuildDiff(value, scope)
	if err != nil {
		t.Fatal(err)
	}
	return value, diff, base, []domain.Entity{tag, skill}
}
