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

func TestRegistryResolvesHistoricalProjectorAfterDefaultChanges(t *testing.T) {
	v1 := Descriptor{SchemaVersion: ProjectionSchemaV1, Version: ProjectorV1, Relations: V1Relations(), Formatter: V1Formatter{}}
	v2 := Descriptor{SchemaVersion: ProjectionSchemaV1, Version: "v2", Relations: V1Relations(), Formatter: V1Formatter{}}
	registry, err := NewRegistry(ProjectionSchemaV1, "v2", v1, v2)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.Default().Version; got != "v2" {
		t.Fatalf("default=%q", got)
	}
	historical, err := registry.Resolve(ProjectionSchemaV1, ProjectorV1)
	if err != nil || historical.Version != ProjectorV1 {
		t.Fatalf("historical=%#v err=%v", historical, err)
	}
	if _, err = registry.Resolve(ProjectionSchemaV1, "retired"); !errors.Is(err, ErrProjectorVersionUnavailable) {
		t.Fatalf("missing historical version fell back: %v", err)
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

func TestV1FormatterHasFrozenFieldsForEveryEntityKind(t *testing.T) {
	formatter := V1Formatter{}
	for _, sample := range []struct {
		kind       domain.EntityKind
		payload    map[string]json.RawMessage
		property   string
		want       any
		collection string
	}{
		{domain.KindAttribute, map[string]json.RawMessage{"value_type": json.RawMessage(`"decimal"`), "default": json.RawMessage(`true`), "unknown": json.RawMessage(`"excluded"`)}, "default", true, ""},
		{domain.KindTag, map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`["z","a"]`)}, "category", "element", "parent_tag_ids"},
		{domain.KindCharacter, map[string]json.RawMessage{"skill_ids": json.RawMessage(`["z","a"]`), "item_ids": json.RawMessage(`[]`)}, "", nil, "skill_ids"},
		{domain.KindSkill, map[string]json.RawMessage{"cooldown": json.RawMessage(`"1.23"`), "effect_ids": json.RawMessage(`["z","a"]`)}, "cooldown", "1.23", "effect_ids"},
		{domain.KindItem, map[string]json.RawMessage{"slot": json.RawMessage(`"hand"`), "effect_ids": json.RawMessage(`[]`), "enhance_tag_ids": json.RawMessage(`["z","a"]`)}, "slot", "hand", "enhance_tag_ids"},
		{domain.KindEffect, map[string]json.RawMessage{"duration": json.RawMessage(`"2"`)}, "duration", "2", ""},
	} {
		entity := domain.Entity{ID: projectorTestID(t), Kind: sample.kind, Key: "key", Name: "Name", Status: domain.StatusActive, SchemaVersion: 1, Payload: sample.payload, Extensions: map[string]json.RawMessage{"unknown": json.RawMessage(`"excluded"`)}}
		_, text, properties, err := formatter.Format(entity)
		if err != nil {
			t.Fatalf("%s: %v", sample.kind, err)
		}
		if sample.property != "" && properties[sample.property] != sample.want {
			t.Fatalf("%s properties=%#v", sample.kind, properties)
		}
		if sample.collection != "" && !reflect.DeepEqual(properties[sample.collection], []string{"a", "z"}) {
			t.Fatalf("%s collection=%#v", sample.kind, properties[sample.collection])
		}
		if _, exists := properties["unknown"]; exists || strings.Contains(text, "excluded") {
			t.Fatalf("%s unknown data leaked: properties=%#v text=%q", sample.kind, properties, text)
		}
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

func TestNodeIDIsSharedWithProjection(t *testing.T) {
	projectID, entityID := projectorTestID(t), projectorTestID(t)
	entity := domain.Entity{ID: entityID, Kind: domain.KindSkill, Key: "skill", Name: "Skill", Status: domain.StatusActive, SchemaVersion: 1}
	want := "urn:eco:" + string(projectID) + ":skill:" + string(entityID)
	if got := NodeID(projectID, entity); got != want {
		t.Fatalf("NodeID()=%q want %q", got, want)
	}
	descriptor := Descriptor{SchemaVersion: ProjectionSchemaV1, Version: ProjectorV1, Relations: V1Relations(), Formatter: V1Formatter{}}
	result, err := Project(descriptor, Revision{ProjectID: projectID, RevisionID: projectorTestID(t), ConfigHash: strings.Repeat("a", 64), Entities: []domain.Entity{entity}})
	if err != nil || len(result.Nodes) != 1 || result.Nodes[0].ID != want {
		t.Fatalf("projection=%#v err=%v", result, err)
	}
}

func TestProjectionIsDeterministicAcrossInputOrderAndRegistryRestart(t *testing.T) {
	project, revision, character, skill, tag := projectorTestID(t), projectorTestID(t), projectorTestID(t), projectorTestID(t), projectorTestID(t)
	base := Revision{
		ProjectID: project, RevisionID: revision, ConfigHash: strings.Repeat("a", 64),
		Entities: []domain.Entity{
			{ID: character, Kind: domain.KindCharacter, Key: "hero", Name: "Hero", TagIDs: []domain.ID{tag}, Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"unknown": json.RawMessage(`"ignored"`)}},
			{ID: skill, Kind: domain.KindSkill, Key: "heal", Name: "Heal", Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"cooldown": json.RawMessage(`"1.23"`), "effect_ids": json.RawMessage(`[]`)}},
			{ID: tag, Kind: domain.KindTag, Key: "water", Name: "Water", Status: domain.StatusActive, SchemaVersion: 1, Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}},
		},
		References: []Reference{
			{SourceID: character, TargetID: skill, TargetKind: domain.KindSkill, FieldPath: "/payload/skill_ids/0", Ordinal: 1},
			{SourceID: character, TargetID: tag, TargetKind: domain.KindTag, FieldPath: "/tag_ids/0", Ordinal: 0},
		},
	}
	var want Result
	var wantBytes, wantHash string
	for iteration := 0; iteration < 4; iteration++ {
		current := base
		current.Entities = append([]domain.Entity(nil), base.Entities...)
		current.References = append([]Reference(nil), base.References...)
		if iteration%2 == 1 {
			current.Entities[0], current.Entities[2] = current.Entities[2], current.Entities[0]
			current.References[0], current.References[1] = current.References[1], current.References[0]
		}
		registry, err := NewRegistry(ProjectionSchemaV1, ProjectorV1, Descriptor{SchemaVersion: ProjectionSchemaV1, Version: ProjectorV1, Relations: V1Relations(), Formatter: V1Formatter{}})
		if err != nil {
			t.Fatal(err)
		}
		result, err := Project(registry.Default(), current)
		if err != nil {
			t.Fatal(err)
		}
		bytes, hash, err := ManifestBytes(result)
		if err != nil {
			t.Fatal(err)
		}
		if iteration == 0 {
			want, wantBytes, wantHash = result, string(bytes), hash
			continue
		}
		if !reflect.DeepEqual(result, want) || string(bytes) != wantBytes || hash != wantHash {
			t.Fatalf("iteration %d drifted: result=%#v bytes=%s hash=%s", iteration, result, bytes, hash)
		}
	}
}

func TestManifestBytesUseProviderJCSOrderAndSortedRecords(t *testing.T) {
	result := Result{Nodes: []Node{{ID: "b", Type: "tag", Label: "B", Text: "B", Properties: map[string]any{"z": "last", "a": "first"}, Provenance: NodeProvenance{}}, {ID: "a", Type: "tag", Label: "A", Text: "A", Properties: map[string]any{}, Provenance: NodeProvenance{}}}, Edges: []Edge{}}
	bytes, hash, err := ManifestBytes(result)
	if err != nil || len(hash) != 64 {
		t.Fatalf("bytes=%s hash=%q err=%v", bytes, hash, err)
	}
	if !strings.HasPrefix(string(bytes), `{"edges":null,"nodes":[{"id":"a","label":"A"`) || !reflect.DeepEqual(result.Nodes[0].ID, "b") {
		t.Fatalf("canonical=%s", bytes)
	}
	again, againHash, err := ManifestBytes(Result{Nodes: []Node{result.Nodes[1], result.Nodes[0]}, Edges: []Edge{}})
	if err != nil || string(bytes) != string(again) || hash != againHash {
		t.Fatalf("stable=%s/%s hash=%s/%s err=%v", bytes, again, hash, againHash, err)
	}
}
