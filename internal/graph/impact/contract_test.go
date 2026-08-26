package impact

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type portFake struct{}

func (portFake) Traverse(context.Context, TraverseRequest, string) (TraverseResponse, error) {
	return TraverseResponse{}, nil
}
func (portFake) Paths(context.Context, PathsRequest, string) (PathsResponse, error) {
	return PathsResponse{}, nil
}
func (portFake) Retrieve(context.Context, RetrieveRequest, string) (RetrieveResponse, error) {
	return RetrieveResponse{}, nil
}

var _ GraphQueryProvider = portFake{}

type idFake struct{ id domain.ID }

func (f idFake) New() (domain.ID, error) { return f.id, nil }

func TestFrozenContractManifest(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(current), "..", "..", "..", "tests", "fixtures", "dependency-impact-v1", "contract.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture ContractManifest
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if err = ValidateManifest(fixture); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceIdentityIsStableAndTyped(t *testing.T) {
	a, err := EvidenceID("node", map[string]any{"id": "a"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := EvidenceID("edge", map[string]any{"id": "a"})
	if a == b || !ValidHash(a) || !ValidHash(b) {
		t.Fatalf("identities node=%q edge=%q", a, b)
	}
}

func TestFixedScenarioFixtureCoversSixKindsNineRelationsAndAdversarialCases(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(current), "..", "..", "..", "tests", "fixtures", "dependency-impact-v1", "scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Version       string   `json:"version"`
		EntityKinds   []string `json:"entity_kinds"`
		Relations     []string `json:"relation_types"`
		RequiredCases []string `json:"required_cases"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	wantKinds := []string{"attribute", "character", "effect", "item", "skill", "tag"}
	wantRelations := []string{"character_has_skill", "character_uses_item", "effect_modifies_attribute", "effect_triggers_effect", "entity_has_tag", "formula_reads_attribute", "item_applies_effect", "item_enhances_tag", "skill_applies_effect"}
	wantCases := []string{"changed_tombstone", "cycle", "direct", "equal_content_checkpoint", "field_formula_provenance", "filtered_start", "multi_level", "parallel_edges", "tag_impact"}
	if fixture.Version != AnalysisContractVersion || !reflect.DeepEqual(fixture.EntityKinds, wantKinds) || !reflect.DeepEqual(fixture.Relations, wantRelations) || !reflect.DeepEqual(fixture.RequiredCases, wantCases) {
		t.Fatalf("scenario drift=%#v", fixture)
	}
}
