package patch

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

func TestDecodeV1BindsFrozenIdentitiesAndCanonicalizesStableOrder(t *testing.T) {
	scope := patchDecodeContext()
	wire := validWirePatch(scope)
	wire.Targets[0], wire.Targets[1] = wire.Targets[1], wire.Targets[0]
	wire.Targets[1].Operations = []aicontract.DraftOperation{
		{Ordinal: 2, Kind: aicontract.OperationReplace, Path: "/payload/damage", Value: json.RawMessage("12"), Evidence: []aicontract.EvidenceID{"evidence-z", "evidence-a"}},
		{Ordinal: 1, Kind: aicontract.OperationReplace, Path: "/payload/cost", Value: json.RawMessage("8"), Evidence: []aicontract.EvidenceID{"evidence-a"}},
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	decoded, canonical, err := DecodeV1(raw, scope)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.Valid() || !decoded.Hash.Valid() || len(canonical) == 0 {
		t.Fatalf("decoded=%#v canonical=%s", decoded, canonical)
	}
	if decoded.ID != scope.PatchID || decoded.Schema != scope.Input.Schema || decoded.Base != scope.Input.Base || decoded.EvidenceManifestIdentity != scope.EvidenceManifestIdentity {
		t.Fatal("frozen identity drift")
	}
	if decoded.Targets[0].EntityID >= decoded.Targets[1].EntityID || decoded.Targets[0].Operations[0].Path != "/payload/cost" || decoded.Targets[0].Operations[1].Path != "/payload/damage" {
		t.Fatalf("canonical order=%#v", decoded.Targets)
	}
	if decoded.Targets[0].Operations[1].Evidence[0] != "evidence-a" {
		t.Fatalf("evidence order=%#v", decoded.Targets[0].Operations[1].Evidence)
	}
	reencoded, err := aicontract.CanonicalDraftPatch(decoded)
	if err != nil || !bytes.Equal(reencoded, canonical) {
		t.Fatalf("canonical mismatch err=%v\n%s\n%s", err, reencoded, canonical)
	}
}

func TestDecodeV1AllowsOnlyDeclaredSchemaCollectionOperations(t *testing.T) {
	scope := patchDecodeContext()
	raw, _ := json.Marshal(validWirePatch(scope))
	if _, _, err := DecodeV1(raw, scope); err != nil {
		t.Fatal(err)
	}
	scope.Values[2].ValueType = JSONString
	scope.Values[2].ElementType = ""
	if _, _, err := DecodeV1(raw, scope); patchErrorCode(err) != ErrorCollectionViolation {
		t.Fatalf("collection err=%v", err)
	}
}

func TestDecodeV1RejectsIdentityAndScopeMismatch(t *testing.T) {
	scope := patchDecodeContext()
	tests := []struct {
		name string
		code ErrorCode
		edit func(*wirePatchV1)
	}{
		{"patch id", ErrorIdentityMismatch, func(v *wirePatchV1) { v.ID = "018f9e40-0000-7000-8000-000000000299" }},
		{"schema", ErrorIdentityMismatch, func(v *wirePatchV1) { v.Schema.Hash = aicontract.Hash(strings.Repeat("f", 64)) }},
		{"base", ErrorIdentityMismatch, func(v *wirePatchV1) { v.Base.GraphContentHash = aicontract.Hash(strings.Repeat("f", 64)) }},
		{"manifest", ErrorIdentityMismatch, func(v *wirePatchV1) { v.EvidenceManifestIdentity.Version = "v2" }},
		{"target", ErrorScopeViolation, func(v *wirePatchV1) { v.Targets[0].EntityID = "018f9e40-0000-7000-8000-000000000299" }},
		{"kind", ErrorScopeViolation, func(v *wirePatchV1) { v.Targets[0].Kind = "release" }},
		{"version", ErrorScopeViolation, func(v *wirePatchV1) { v.Targets[0].ExpectedEntityVersion++ }},
		{"path", ErrorScopeViolation, func(v *wirePatchV1) { v.Targets[0].Operations[0].Path = "/status" }},
		{"operation", ErrorScopeViolation, func(v *wirePatchV1) { v.Targets[0].Operations[0].Kind = aicontract.OperationRemove }},
		{"evidence", ErrorEvidenceViolation, func(v *wirePatchV1) { v.Targets[0].Operations[0].Evidence = []aicontract.EvidenceID{"fabricated"} }},
		{"ordinal", ErrorSchemaViolation, func(v *wirePatchV1) { v.Targets[0].Operations[0].Ordinal = 3 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wire := validWirePatch(scope)
			test.edit(&wire)
			raw, _ := json.Marshal(wire)
			if _, _, err := DecodeV1(raw, scope); patchErrorCode(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func patchDecodeContext() DecodeContext {
	hash := aicontract.Hash(strings.Repeat("a", 64))
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000201")
	revisionID := aicontract.RevisionID("018f9e40-0000-7000-8000-000000000202")
	firstID := aicontract.EntityID("018f9e40-0000-7000-8000-000000000203")
	secondID := aicontract.EntityID("018f9e40-0000-7000-8000-000000000204")
	fixture := aicontract.V1Fixture()
	input := aicontract.AIDesignInputV1{
		Schema: fixture.PatchSchema.Identity,
		Base: aicontract.FrozenBaseIdentity{
			ProjectID: projectID, ConfigRevisionID: revisionID, ConfigHash: hash, VersionManifestHash: hash,
			MaterializationHash: hash, GraphNamespace: string(projectID), GraphSnapshot: string(revisionID), GraphContentHash: hash,
		},
		Baseline: aicontract.BaselineIdentity{Kind: aicontract.BaselineNone},
		Goals:    []aicontract.Goal{{ID: "balance", Description: "Balance the selected entities."}},
		AllowedTargets: []aicontract.AllowedTarget{
			{
				EntityID: firstID, Kind: "skill", ExpectedEntityVersion: 2,
				Paths: []aicontract.AllowedPath{
					{Path: "/payload/cost", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}},
					{Path: "/payload/damage", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}},
				},
			},
			{
				EntityID: secondID, Kind: "skill", ExpectedEntityVersion: 3,
				Paths: []aicontract.AllowedPath{{Path: "/payload/effect_ids", Operations: []aicontract.PatchOperationKind{aicontract.OperationAdd, aicontract.OperationRemove}}},
			},
		},
		Scenes: []string{"single-target-30s"}, Budget: aicontract.Budget{Policy: fixture.Budget.Identity, BudgetLimits: fixture.Budget.Limits},
		RequiredVersions: []aicontract.VersionIdentity{{ID: "validation", Version: "v1", Hash: hash}},
	}
	return DecodeContext{
		PatchID: "018f9e40-0000-7000-8000-000000000205", Input: input,
		EvidenceManifestIdentity: aicontract.VersionIdentity{ID: "retrieval-evidence", Version: "v1", Hash: hash},
		EvidenceIDs:              []aicontract.EvidenceID{"evidence-a", "evidence-z"},
		Values: []ValueScope{
			{EntityID: firstID, Path: "/payload/cost", ValueType: JSONInteger, Original: json.RawMessage("10")},
			{EntityID: firstID, Path: "/payload/damage", ValueType: JSONInteger, Original: json.RawMessage("10")},
			{EntityID: secondID, Path: "/payload/effect_ids", ValueType: JSONArray, ElementType: JSONString, Original: json.RawMessage("[]")},
		},
	}
}

func validWirePatch(scope DecodeContext) wirePatchV1 {
	return wirePatchV1{
		ID: scope.PatchID, Schema: scope.Input.Schema, Base: scope.Input.Base, EvidenceManifestIdentity: scope.EvidenceManifestIdentity,
		Targets: []aicontract.DraftTarget{
			{
				EntityID: scope.Input.AllowedTargets[0].EntityID, Kind: "skill", ExpectedEntityVersion: 2,
				Operations: []aicontract.DraftOperation{{Ordinal: 1, Kind: aicontract.OperationReplace, Path: "/payload/cost", Value: json.RawMessage("8"), Evidence: []aicontract.EvidenceID{"evidence-a"}}},
			},
			{
				EntityID: scope.Input.AllowedTargets[1].EntityID, Kind: "skill", ExpectedEntityVersion: 3,
				Operations: []aicontract.DraftOperation{{Ordinal: 1, Kind: aicontract.OperationAdd, Path: "/payload/effect_ids", Value: json.RawMessage("\"018f9e40-0000-7000-8000-000000000206\""), Evidence: []aicontract.EvidenceID{"evidence-z"}}},
			},
		},
		Rationale: "Use the pinned evidence to adjust the selected values.", Assumptions: []string{"The fixed scene remains representative."},
	}
}

func patchErrorCode(err error) ErrorCode {
	var typed *DecodeError
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}
