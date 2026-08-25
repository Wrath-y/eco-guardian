package retrieval

import (
	"encoding/json"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

func TestBuildV1RequestUsesOnlyFrozenInputAndRegisteredLimits(t *testing.T) {
	input := retrievalInput(t)
	request, err := BuildV1Request(input)
	if err != nil {
		t.Fatal(err)
	}
	if request.Base != input.Base || request.Budget != input.Budget || request.Base.GraphSnapshot != string(input.Base.ConfigRevisionID) {
		t.Fatalf("request identity=%#v", request)
	}
	if len(request.Filters.NodeTypes) != 2 || request.Filters.NodeTypes[0] != "character" || request.Filters.NodeTypes[1] != "skill" || len(request.Filters.EdgeTypes) != 0 {
		t.Fatalf("filters=%#v", request.Filters)
	}
	for _, expected := range []string{"goal tune", "metric metric-dps", "constraint cap", "scene single-target-30s"} {
		if !strings.Contains(request.Query, expected) {
			t.Fatalf("query %q missing %q", request.Query, expected)
		}
	}
	changed := input
	changed.Goals = append([]aicontract.Goal(nil), input.Goals...)
	changed.Goals[0].Description = "A different frozen goal."
	second, err := BuildV1Request(changed)
	if err != nil || second.Query == request.Query {
		t.Fatalf("changed request=%#v err=%v", second, err)
	}
}

func retrievalInput(t *testing.T) aicontract.AIDesignInputV1 {
	t.Helper()
	hash := aicontract.Hash(strings.Repeat("a", 64))
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000201")
	revisionID := aicontract.RevisionID("018f9e40-0000-7000-8000-000000000202")
	fixture := aicontract.V1Fixture()
	return aicontract.AIDesignInputV1{
		Schema:      fixture.PatchSchema.Identity,
		Base:        aicontract.FrozenBaseIdentity{ProjectID: projectID, ConfigRevisionID: revisionID, ConfigHash: hash, VersionManifestHash: hash, MaterializationHash: hash, GraphNamespace: string(projectID), GraphSnapshot: string(revisionID), GraphContentHash: hash},
		Baseline:    aicontract.BaselineIdentity{Kind: aicontract.BaselineNone},
		Goals:       []aicontract.Goal{{ID: "tune", Description: "Tune damage."}},
		Metrics:     []aicontract.MetricGoal{{MetricID: "metric-dps", Version: "v1", Direction: aicontract.MetricMinimize, Unit: "points_per_second"}},
		Constraints: []aicontract.Constraint{{ID: "cap", Path: "/payload/cost", Operator: aicontract.ConstraintLessOrEqual, Value: json.RawMessage(`10`)}},
		AllowedTargets: []aicontract.AllowedTarget{
			{EntityID: "018f9e40-0000-7000-8000-000000000203", Kind: "skill", ExpectedEntityVersion: 2, Paths: []aicontract.AllowedPath{{Path: "/payload/cost", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}},
			{EntityID: "018f9e40-0000-7000-8000-000000000204", Kind: "character", ExpectedEntityVersion: 3, Paths: []aicontract.AllowedPath{{Path: "/payload/attribute_values/0/expression", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}},
		},
		Scenes: []string{"single-target-30s"}, Budget: aicontract.Budget{Policy: fixture.Budget.Identity, BudgetLimits: fixture.Budget.Limits},
		RequiredVersions: []aicontract.VersionIdentity{{ID: "validation", Version: "v1", Hash: hash}},
	}
}
