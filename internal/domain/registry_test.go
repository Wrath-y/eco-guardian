package domain

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestRegistryContainsSchemasAndRejectsUnregisteredRules(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Kinds()) != 6 {
		t.Fatalf("kinds = %v", r.Kinds())
	}
	if _, ok := r.Schema(KindSkill); !ok {
		t.Fatal("skill schema missing")
	}
	e, _ := NewEntity(KindEffect, EntityDraft{Key: "burn", Name: "Burn", Payload: map[string]json.RawMessage{
		"duration": json.RawMessage("1"), "modifiers": json.RawMessage(`[{"attribute_id":"018f9e40-0000-7000-8000-000000000001","operation":"Bogus","value":1}]`), "trigger_blocks": json.RawMessage(`[{"event":"on_bad","target":{"type":"bad"}}]`), "stack_rule": json.RawMessage(`{"operation":"Add","max_stacks":1,"refresh_policy":"refresh"}`),
	}}, time.Now())
	if len(r.Validate(e)) < 2 {
		t.Fatalf("expected validation issues, got %v", r.Validate(e))
	}
}

func TestBuiltInFixtureValidation(t *testing.T) {
	r, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		valid bool
	}{{"valid", true}, {"invalid", false}} {
		raw, err := os.ReadFile("../../tests/fixtures/domain/" + tc.name + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var fixtures []struct {
			Kind    EntityKind                 `json:"kind"`
			Key     string                     `json:"key"`
			Name    string                     `json:"name"`
			Payload map[string]json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(raw, &fixtures); err != nil {
			t.Fatal(err)
		}
		if len(fixtures) != 6 {
			t.Fatalf("%s fixture should cover all kinds", tc.name)
		}
		for _, fixture := range fixtures {
			e := Entity{Kind: fixture.Kind, Key: fixture.Key, Name: fixture.Name, Status: StatusActive, SchemaVersion: 1, Payload: fixture.Payload, Extensions: map[string]json.RawMessage{}}
			if got := len(r.Validate(e)) == 0; got != tc.valid {
				t.Errorf("%s/%s valid=%v issues=%v", tc.name, fixture.Kind, got, r.Validate(e))
			}
		}
	}
}

func TestCanonicalIndexesCanBeRebuilt(t *testing.T) {
	id, _ := NewID()
	skill, _ := NewID()
	tag, _ := NewID()
	e := Entity{ID: id, Kind: KindCharacter, Key: "hero", Name: "Hero", TagIDs: []ID{tag}, Status: StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"attribute_values": json.RawMessage("[]"), "skill_ids": json.RawMessage(`[` + quote(string(skill)) + `]`), "item_ids": json.RawMessage("[]"), "rule_blocks": json.RawMessage("[]")}, Extensions: map[string]json.RawMessage{}, EntityVersion: 1}
	hash, canonical, err := BlobHash(e)
	if err != nil || hash == "" {
		t.Fatalf("hash: %s %v", hash, err)
	}
	var restored Entity
	if err := json.Unmarshal(canonical, &restored); err != nil {
		t.Fatal(err)
	}
	before, tags := ExtractIndexes(e)
	after, _ := ExtractIndexes(restored)
	if len(before) != len(after) || len(tags) != 1 || before[1].TargetID != after[1].TargetID {
		t.Fatalf("indexes differ: %#v %#v", before, after)
	}
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
