package projector

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func projectorTestID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRegistryFreezesV1AndNeverFallsBack(t *testing.T) {
	descriptor := Descriptor{SchemaVersion: ProjectionSchemaV1, Version: ProjectorV1, Relations: V1Relations(), Formatter: V1Formatter{}}
	registry, err := NewRegistry(ProjectionSchemaV1, ProjectorV1, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = registry.Resolve("historical", "missing"); !errors.Is(err, ErrProjectorVersionUnavailable) {
		t.Fatalf("err=%v", err)
	}
	if _, err = NewRegistry(ProjectionSchemaV1, ProjectorV1, descriptor, descriptor); err == nil {
		t.Fatal("duplicate projector identity was accepted")
	}
	if got, want := RelationTokens(registry.Default().Relations), []RelationToken{CharacterHasSkill, CharacterUsesItem, EffectModifiesAttribute, EffectTriggersEffect, EntityHasTag, FormulaReadsAttribute, ItemAppliesEffect, ItemEnhancesTag, SkillAppliesEffect}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens=%v want=%v", got, want)
	}
}

func TestV1FormatterIsDeterministicAndExcludesExtensions(t *testing.T) {
	entity := domain.Entity{ID: projectorTestID(t), Kind: domain.KindItem, Key: "sword", Name: "Sword", Description: "Reliable", TagIDs: []domain.ID{projectorTestID(t), projectorTestID(t)}, Status: domain.StatusActive, SchemaVersion: 1, Extensions: map[string]json.RawMessage{"vendor.example/private": json.RawMessage(`{"secret":"ignored"}`)}, EntityVersion: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	label, text, properties, err := (V1Formatter{}).Format(entity)
	if err != nil {
		t.Fatal(err)
	}
	if label != "Sword" || text != "item\nSword\nReliable" || properties["name"] != "Sword" {
		t.Fatalf("label=%q text=%q properties=%#v", label, text, properties)
	}
	if _, exists := properties["extensions"]; exists {
		t.Fatal("unknown extension entered Graph properties")
	}
}

func TestProjectMaterializesStableNodesAndRegisteredEdgesOnly(t *testing.T) {
	project, character, skill, item := projectorTestID(t), projectorTestID(t), projectorTestID(t), projectorTestID(t)
	descriptor := Descriptor{SchemaVersion: ProjectionSchemaV1, Version: ProjectorV1, Relations: V1Relations(), Formatter: V1Formatter{}}
	revision := Revision{ProjectID: project, RevisionID: projectorTestID(t), ConfigHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Entities: []domain.Entity{{ID: skill, Kind: domain.KindSkill, Key: "s", Name: "Skill", Status: domain.StatusActive, SchemaVersion: 1}, {ID: character, Kind: domain.KindCharacter, Key: "c", Name: "Character", Status: domain.StatusActive, SchemaVersion: 1}, {ID: item, Kind: domain.KindItem, Key: "i", Name: "Archived", Status: domain.StatusArchived, SchemaVersion: 1}}, References: []Reference{{SourceID: character, TargetID: skill, TargetKind: domain.KindSkill, FieldPath: "/payload/skill_ids/0", Ordinal: 0}, {SourceID: character, TargetID: item, TargetKind: domain.KindItem, FieldPath: "/payload/item_ids/0", Ordinal: 1}}}
	result, err := Project(descriptor, revision)
	if err == nil {
		t.Fatalf("archived endpoint must fail, got %#v", result)
	}
	revision.References = revision.References[:1]
	result, err = Project(descriptor, revision)
	if err != nil || len(result.Nodes) != 2 || len(result.Edges) != 1 || result.Edges[0].RelationKind != "explicit" || result.Edges[0].Confidence != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if result.Nodes[0].ID > result.Nodes[1].ID || len(result.Edges[0].ID) != 52 {
		t.Fatalf("unstable records=%#v", result)
	}
}

func TestManifestBytesUseProviderJCSOrderAndSortedRecords(t *testing.T) {
	result := Result{Nodes: []Node{{ID: "b", Type: "tag", Label: "B", Text: "B", Properties: map[string]any{"z": "last", "a": "first"}, Provenance: NodeProvenance{}}, {ID: "a", Type: "tag", Label: "A", Text: "A", Properties: map[string]any{}, Provenance: NodeProvenance{}}}, Edges: []Edge{}}
	bytes, hash, err := ManifestBytes(result)
	if err != nil || len(hash) != 64 {
		t.Fatalf("bytes=%s hash=%q err=%v", bytes, hash, err)
	}
	if !strings.HasPrefix(string(bytes), `{"edges":[],"nodes":[{"id":"a","label":"A"`) || !reflect.DeepEqual(result.Nodes[0].ID, "b") {
		t.Fatalf("canonical=%s", bytes)
	}
	again, againHash, err := ManifestBytes(Result{Nodes: []Node{result.Nodes[1], result.Nodes[0]}, Edges: []Edge{}})
	if err != nil || string(bytes) != string(again) || hash != againHash {
		t.Fatalf("stable=%s/%s hash=%s/%s err=%v", bytes, again, hash, againHash, err)
	}
}
