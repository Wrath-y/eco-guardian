package patch

import (
	"bytes"
	"encoding/json"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestBuildDecodeContextUsesRegisteredSchemaAndFrozenOriginals(t *testing.T) {
	entity := schemaSkillEntity([]string{"018f9e40-0000-7000-8000-000000000220"})
	paths := []aicontract.AllowedPath{
		{Path: "/payload/cooldown", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}},
		{Path: "/payload/effect_ids", Operations: []aicontract.PatchOperationKind{aicontract.OperationAdd, aicontract.OperationRemove}},
		{Path: "/payload/target_selector", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}},
	}
	scope := schemaContext(t, entity, paths)
	wire := schemaWire(scope, []aicontract.DraftOperation{
		{Ordinal: 1, Kind: aicontract.OperationReplace, Path: "/payload/cooldown", Value: json.RawMessage("\"1000\""), Evidence: []aicontract.EvidenceID{"evidence-a"}},
		{Ordinal: 2, Kind: aicontract.OperationAdd, Path: "/payload/effect_ids", Value: json.RawMessage("\"018f9e40-0000-7000-8000-000000000221\""), Evidence: []aicontract.EvidenceID{"evidence-a"}},
		{Ordinal: 3, Kind: aicontract.OperationReplace, Path: "/payload/target_selector", Value: json.RawMessage("{\"type\":\"self\"}"), Evidence: []aicontract.EvidenceID{"evidence-a"}},
	})
	raw, _ := json.Marshal(wire)
	decoded, _, err := DecodeV1(raw, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Targets) != 1 || len(decoded.Targets[0].Operations) != 3 || scope.Registry == nil || len(scope.Values) != 3 {
		t.Fatalf("decoded=%#v scope=%#v", decoded, scope)
	}
}

func TestBuildDecodeContextResolvesEachRegisteredPathShape(t *testing.T) {
	entity := schemaSkillEntity([]string{"018f9e40-0000-7000-8000-000000000220"})
	for _, path := range []aicontract.FieldPath{"/payload/cooldown", "/payload/effect_ids", "/payload/target_selector"} {
		t.Run(string(path), func(t *testing.T) {
			operations := []aicontract.PatchOperationKind{aicontract.OperationReplace}
			if path == "/payload/effect_ids" {
				operations = []aicontract.PatchOperationKind{aicontract.OperationAdd}
			}
			_ = schemaContext(t, entity, []aicontract.AllowedPath{{Path: path, Operations: operations}})
		})
	}
}

func TestDecodeV1RejectsUnknownDuplicateForbiddenAndUnauthorizedMembers(t *testing.T) {
	entity := schemaSkillEntity(nil)
	scope := schemaContext(t, entity, []aicontract.AllowedPath{{Path: "/payload/target_selector", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}})
	wire := schemaWire(scope, []aicontract.DraftOperation{{Ordinal: 1, Kind: aicontract.OperationReplace, Path: "/payload/target_selector", Value: json.RawMessage("{\"type\":\"self\"}"), Evidence: []aicontract.EvidenceID{"evidence-a"}}})
	base, _ := json.Marshal(wire)
	tests := []struct {
		name string
		raw  []byte
		code ErrorCode
	}{
		{"unknown top", bytes.Replace(base, []byte("\"rationale\":"), []byte("\"release\":true,\"rationale\":"), 1), ErrorSchemaViolation},
		{"duplicate top", bytes.Replace(base, []byte("\"rationale\":"), []byte("\"rationale\":\"duplicate\",\"rationale\":"), 1), ErrorSchemaViolation},
		{"unknown operation", bytes.Replace(base, []byte("\"value\":"), []byte("\"create_revision\":true,\"value\":"), 1), ErrorSchemaViolation},
		{"duplicate value", bytes.Replace(base, []byte("{\"type\":\"self\"}"), []byte("{\"type\":\"self\",\"type\":\"source\"}"), 1), ErrorSchemaViolation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := DecodeV1(test.raw, scope); patchErrorCode(err) != test.code {
				t.Fatalf("err=%v raw=%s", err, test.raw)
			}
		})
	}

	for _, path := range []aicontract.FieldPath{"/id", "/status", "/entity_version", "/extensions/vendor/flag", "/release", "/registry"} {
		t.Run(string(path), func(t *testing.T) {
			malicious := patchDecodeContext()
			malicious.Input.AllowedTargets[0].Paths[0].Path = path
			malicious.Values[0].Path = path
			malicious.Values[0].ValueType = JSONString
			malicious.Values[0].Original = json.RawMessage("\"old\"")
			wire := validWirePatch(malicious)
			wire.Targets[0].Operations[0].Path = path
			wire.Targets[0].Operations[0].Value = json.RawMessage("\"new\"")
			raw, _ := json.Marshal(wire)
			if _, _, err := DecodeV1(raw, malicious); patchErrorCode(err) != ErrorScopeViolation {
				t.Fatalf("path=%s err=%v", path, err)
			}
		})
	}
}

func TestDecodeV1RejectsSchemaTypeEnumNumericAndUnitViolations(t *testing.T) {
	skill := schemaSkillEntity(nil)
	targetScope := schemaContext(t, skill, []aicontract.AllowedPath{{Path: "/payload/target_selector", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}})
	for _, test := range []struct {
		name  string
		value string
		code  ErrorCode
	}{
		{"type", "42", ErrorTypeViolation},
		{"enum", "{\"type\":\"unknown\"}", ErrorSchemaViolation},
		{"field", "{\"type\":\"self\",\"credential\":\"x\"}", ErrorSchemaViolation},
	} {
		t.Run(test.name, func(t *testing.T) {
			wire := schemaWire(targetScope, []aicontract.DraftOperation{{Ordinal: 1, Kind: aicontract.OperationReplace, Path: "/payload/target_selector", Value: json.RawMessage(test.value), Evidence: []aicontract.EvidenceID{"evidence-a"}}})
			raw, _ := json.Marshal(wire)
			if _, _, err := DecodeV1(raw, targetScope); patchErrorCode(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}

	attribute := schemaAttributeEntity()
	for _, test := range []struct {
		path  aicontract.FieldPath
		value string
		code  ErrorCode
	}{
		{"/payload/default", "\"1.0\"", ErrorNumericViolation},
		{"/payload/default", "1", ErrorTypeViolation},
		{"/payload/base_unit", "\"unknown_unit\"", ErrorUnitViolation},
		{"/payload/base_unit", "\"health_point\"", ErrorUnitViolation},
	} {
		scope := schemaContext(t, attribute, []aicontract.AllowedPath{{Path: test.path, Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}})
		wire := schemaWire(scope, []aicontract.DraftOperation{{Ordinal: 1, Kind: aicontract.OperationReplace, Path: test.path, Value: json.RawMessage(test.value), Evidence: []aicontract.EvidenceID{"evidence-a"}}})
		raw, _ := json.Marshal(wire)
		if _, _, err := DecodeV1(raw, scope); patchErrorCode(err) != test.code {
			t.Fatalf("path=%s value=%s err=%v", test.path, test.value, err)
		}
	}
}

func TestDecodeV1RejectsAmbiguousCollectionOperations(t *testing.T) {
	existing := "018f9e40-0000-7000-8000-000000000220"
	missing := "018f9e40-0000-7000-8000-000000000221"
	tests := []struct {
		name     string
		original []string
		kind     aicontract.PatchOperationKind
		value    string
	}{
		{"add duplicate", []string{existing}, aicontract.OperationAdd, existing},
		{"remove missing", []string{existing}, aicontract.OperationRemove, missing},
		{"remove duplicate", []string{existing, existing}, aicontract.OperationRemove, existing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entity := schemaSkillEntity(test.original)
			scope := schemaContext(t, entity, []aicontract.AllowedPath{{Path: "/payload/effect_ids", Operations: []aicontract.PatchOperationKind{aicontract.OperationAdd, aicontract.OperationRemove}}})
			value, _ := json.Marshal(test.value)
			wire := schemaWire(scope, []aicontract.DraftOperation{{Ordinal: 1, Kind: test.kind, Path: "/payload/effect_ids", Value: value, Evidence: []aicontract.EvidenceID{"evidence-a"}}})
			raw, _ := json.Marshal(wire)
			if _, _, err := DecodeV1(raw, scope); patchErrorCode(err) != ErrorArrayAmbiguous {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func schemaContext(t *testing.T, entity domain.Entity, paths []aicontract.AllowedPath) DecodeContext {
	t.Helper()
	base := patchDecodeContext()
	base.Input.AllowedTargets = []aicontract.AllowedTarget{{
		EntityID: aicontract.EntityID(entity.ID), Kind: string(entity.Kind), ExpectedEntityVersion: entity.EntityVersion, Paths: paths,
	}}
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := BuildDecodeContext(base.PatchID, base.Input, base.EvidenceManifestIdentity, base.EvidenceIDs, registry, []domain.Entity{entity})
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func schemaWire(scope DecodeContext, operations []aicontract.DraftOperation) wirePatchV1 {
	target := scope.Input.AllowedTargets[0]
	return wirePatchV1{
		ID: scope.PatchID, Schema: scope.Input.Schema, Base: scope.Input.Base, EvidenceManifestIdentity: scope.EvidenceManifestIdentity,
		Targets:   []aicontract.DraftTarget{{EntityID: target.EntityID, Kind: target.Kind, ExpectedEntityVersion: target.ExpectedEntityVersion, Operations: operations}},
		Rationale: "Apply only the evidence-backed scoped changes.", Assumptions: []string{"The frozen base remains the comparison point."},
	}
}

func schemaSkillEntity(effectIDs []string) domain.Entity {
	effects, _ := json.Marshal(effectIDs)
	return domain.Entity{
		ID: "018f9e40-0000-7000-8000-000000000203", Kind: domain.KindSkill, EntityVersion: 2,
		Payload: map[string]json.RawMessage{
			"costs": json.RawMessage("[]"), "cooldown": json.RawMessage("\"500\""),
			"target_selector": json.RawMessage("{\"type\":\"primary_target\"}"), "effect_ids": effects, "rule_blocks": json.RawMessage("[]"),
		},
	}
}

func schemaAttributeEntity() domain.Entity {
	return domain.Entity{
		ID: "018f9e40-0000-7000-8000-000000000203", Kind: domain.KindAttribute, EntityVersion: 2,
		Payload: map[string]json.RawMessage{
			"value_type": json.RawMessage("\"decimal\""), "dimension": json.RawMessage("\"damage\""),
			"base_unit": json.RawMessage("\"damage_point\""), "default": json.RawMessage("\"1\""),
		},
	}
}
