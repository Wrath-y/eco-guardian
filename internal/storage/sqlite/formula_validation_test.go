package sqlite

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestLocalValidationResolvesAttributeKeysAndBlocksMixedDimensions(t *testing.T) {
	schemas, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store, _, err := Create(context.Background(), t.TempDir(), schemas)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	health, _, err := store.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{
		Key: "health", Name: "Health", Payload: map[string]json.RawMessage{
			"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"health"`),
			"base_unit": json.RawMessage(`"health_point"`), "default": json.RawMessage(`"100"`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	characterPayload := func(expression string) map[string]json.RawMessage {
		binding, _ := json.Marshal([]map[string]string{{"output_attribute_id": string(health.ID), "expression": expression}})
		return map[string]json.RawMessage{
			"attribute_values": binding, "skill_ids": json.RawMessage(`[]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`),
		}
	}
	_, validRevision, err := store.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "valid_hero", Name: "Valid", Payload: characterPayload(`${self:health} + 10[health_point]`)})
	if err != nil {
		t.Fatal(err)
	}
	if validRevision.Validation == nil || validRevision.Validation.Block != 0 {
		t.Fatalf("valid formula summary=%#v", validRevision.Validation)
	}
	var symbol string
	if err = store.db.QueryRow(`SELECT symbol FROM formula_index WHERE revision_id=?`, validRevision.ID).Scan(&symbol); err != nil || symbol != "health" {
		t.Fatalf("formula symbol=%q err=%v", symbol, err)
	}

	_, invalidRevision, err := store.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "invalid_hero", Name: "Invalid", Payload: characterPayload(`80[health_point] + 10[damage_point]`)})
	if err != nil {
		t.Fatal(err)
	}
	if invalidRevision.Validation == nil || invalidRevision.Validation.Block != 1 {
		t.Fatalf("invalid formula summary=%#v", invalidRevision.Validation)
	}
}
