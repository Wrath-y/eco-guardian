package validation

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

func TestAnalyzeV1BindsImplementationsAndReturnsDetachedStableIssues(t *testing.T) {
	schemas, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := formula.V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	versions, err := V1VersionManifest(registry)
	if err != nil {
		t.Fatal(err)
	}
	entities := analyzerEntities()
	first, err := AnalyzeV1(context.Background(), schemas, registry, versions, entities, ScopeFull)
	if err != nil {
		t.Fatal(err)
	}
	entities[0], entities[1] = entities[1], entities[0]
	second, err := AnalyzeV1(context.Background(), schemas, registry, versions, entities, ScopeFull)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) || len(first) != 1 || first[0].Code != "REFERENCE_NOT_FOUND" || first[0].Evidence["target"] == "" {
		t.Fatalf("first=%#v second=%#v", first, second)
	}
	first[0].Evidence["target"] = "model replacement"
	if second[0].Evidence["target"] == "model replacement" {
		t.Fatal("analysis issue evidence shared mutable storage")
	}
	versions.Registry = "wrong-registry"
	if _, err := AnalyzeV1(context.Background(), schemas, registry, versions, entities, ScopeFull); err == nil {
		t.Fatal("mismatched implementation versions were accepted")
	}
}

func analyzerEntities() []domain.Entity {
	now := time.Unix(1_700_000_000, 0).UTC()
	return []domain.Entity{
		{
			ID: "018f9e40-0000-7000-8000-000000000209", Kind: domain.KindTag, Key: "tag", Name: "Tag", Status: domain.StatusActive,
			SchemaVersion: 1, Payload: map[string]json.RawMessage{"category": json.RawMessage(`"test"`), "parent_tag_ids": json.RawMessage(`[]`)},
			Extensions: map[string]json.RawMessage{}, TagIDs: []domain.ID{}, EntityVersion: 1, CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "018f9e40-0000-7000-8000-000000000210", Kind: domain.KindSkill, Key: "skill", Name: "Skill", Status: domain.StatusActive,
			SchemaVersion: 1, Payload: map[string]json.RawMessage{
				"costs": json.RawMessage(`[]`), "cooldown": json.RawMessage(`"1000"`), "target_selector": json.RawMessage(`{"type":"primary_target"}`),
				"effect_ids": json.RawMessage(`["018f9e40-0000-7000-8000-000000000221"]`), "rule_blocks": json.RawMessage(`[]`),
			},
			Extensions: map[string]json.RawMessage{}, TagIDs: []domain.ID{}, EntityVersion: 3, CreatedAt: now, UpdatedAt: now,
		},
	}
}
