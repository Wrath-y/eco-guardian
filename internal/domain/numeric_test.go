package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeNumericEntityUsesCanonicalStrings(t *testing.T) {
	entity := Entity{Payload: map[string]json.RawMessage{"duration": json.RawMessage(`"0010"`), "stack_rule": json.RawMessage(`{"max_stacks":"01","cap":"1.200"}`)}}
	normalized, err := NormalizeNumericEntity(entity)
	if err != nil {
		t.Fatal(err)
	}
	if string(normalized.Payload["duration"]) != `"10"` || !strings.Contains(string(normalized.Payload["stack_rule"]), `"max_stacks":"1"`) || !strings.Contains(string(normalized.Payload["stack_rule"]), `"cap":"1.2"`) {
		t.Fatalf("not canonical: %#v", normalized.Payload)
	}
}
func TestNormalizeNumericEntityRejectsJSONFloat(t *testing.T) {
	_, err := NormalizeNumericEntity(Entity{Payload: map[string]json.RawMessage{"duration": json.RawMessage(`1`)}})
	if err == nil {
		t.Fatal("JSON float accepted")
	}
}
