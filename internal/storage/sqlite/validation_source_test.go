package sqlite

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

func TestMaterializedValidationSourceIsRevisionStable(t *testing.T) {
	store := newStore(t)
	entity, revision, err := store.Create(context.Background(), domain.KindAttribute, domain.EntityDraft{Key: "power", Name: "Power", Payload: map[string]json.RawMessage{"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"damage"`), "base_unit": json.RawMessage(`"damage_point"`), "default": json.RawMessage(`"1"`)}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.MaterializeValidationSource(context.Background(), validation.SourceRevision, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Entities) != 1 || snapshot.Source.InputHash != revision.ConfigHash {
		t.Fatalf("%#v", snapshot)
	}
	_, _, err = store.Patch(context.Background(), domain.KindAttribute, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"Renamed"`)})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Entities[0].Name != "Power" {
		t.Fatalf("snapshot changed: %#v", snapshot.Entities[0])
	}
	working, err := store.MaterializeValidationSource(context.Background(), validation.SourceWorking, "")
	if err != nil || working.Source.InputHash == snapshot.Source.InputHash {
		t.Fatalf("%#v %v", working, err)
	}
}
