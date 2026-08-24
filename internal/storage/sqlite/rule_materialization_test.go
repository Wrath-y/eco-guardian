package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/rules/materialization"
)

func TestReadRuleSourceRequiresExactFullValidation(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{Key: "power", Name: "Power", Payload: map[string]json.RawMessage{"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"damage"`), "base_unit": json.RawMessage(`"damage_point"`), "default": json.RawMessage(`"1"`)}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.ReadRuleSource(context.Background(), revision.ID)
	var diagnostic materialization.Diagnostic
	if !errors.As(err, &diagnostic) || diagnostic.Code != materialization.DiagnosticValidationRequired {
		t.Fatalf("err=%v", err)
	}
	if err = store.RunFullValidation(context.Background(), revision.ID); err != nil {
		t.Fatal(err)
	}
	source, err := store.ReadRuleSource(context.Background(), revision.ID)
	if err != nil || !source.Valid() || source.RevisionID != revision.ID || source.ConfigHash != revision.ConfigHash || len(source.Entities) != 1 {
		t.Fatalf("source=%+v err=%v", source, err)
	}
}
