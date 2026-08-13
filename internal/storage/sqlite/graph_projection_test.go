package sqlite

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
)

func TestReadProjectionRevisionUsesExactImmutableGraphInputs(t *testing.T) {
	store := newStore(t)
	tag, _, err := store.Create(context.Background(), domain.KindTag, tagDraft("water"))
	if err != nil {
		t.Fatal(err)
	}
	character, revision, err := store.Create(context.Background(), domain.KindCharacter, domain.EntityDraft{Key: "hero", Name: "Before", TagIDs: []domain.ID{tag.ID}, Payload: map[string]json.RawMessage{"attribute_values": json.RawMessage(`[]`), "skill_ids": json.RawMessage(`[]`), "item_ids": json.RawMessage(`[]`), "rule_blocks": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	input, err := store.ReadProjectionRevision(context.Background(), revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if input.ProjectID != store.ProjectID() || input.RevisionID != revision.ID || input.ConfigHash != revision.ConfigHash || len(input.Entities) != 2 || len(input.References) != 1 {
		t.Fatalf("input=%#v", input)
	}
	if input.References[0].SourceID != character.ID || input.References[0].TargetID != tag.ID || input.References[0].FieldPath != "/tag_ids/0" || input.References[0].Ordinal < 0 {
		t.Fatalf("references=%#v", input.References)
	}
	if _, _, err = store.Patch(context.Background(), domain.KindCharacter, character.ID, character.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"After"`)}); err != nil {
		t.Fatal(err)
	}
	if input.Entities[0].Name == "After" || input.Entities[1].Name == "After" {
		t.Fatalf("immutable input fell back to working state: %#v", input.Entities)
	}

	archived, archiveRevision, err := store.Delete(context.Background(), domain.KindCharacter, character.ID, character.EntityVersion+1)
	if err != nil {
		t.Fatal(err)
	}
	archiveInput, err := store.ReadProjectionRevision(context.Background(), archiveRevision.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundArchived := false
	for _, entity := range archiveInput.Entities {
		if entity.ID == archived.ID {
			foundArchived = entity.Status == domain.StatusArchived && entity.SchemaVersion == archived.SchemaVersion
		}
	}
	if !foundArchived {
		t.Fatalf("archived entity/schema missing from immutable input: %#v", archiveInput.Entities)
	}
}

var _ projector.RevisionReader = (*Store)(nil)
