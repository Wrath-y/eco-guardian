package report

import (
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
)

func freshnessInput(t *testing.T) impact.Input {
	t.Helper()
	projectID, _ := domain.NewID()
	baseID, _ := domain.NewID()
	targetID, _ := domain.NewID()
	hash := strings.Repeat("a", 64)
	return impact.Input{ProjectID: projectID, Base: impact.RevisionIdentity{RevisionID: baseID, ConfigHash: hash, VersionManifestHash: hash, GraphManifestHash: hash}, Target: impact.RevisionIdentity{RevisionID: targetID, ConfigHash: hash, VersionManifestHash: hash, GraphManifestHash: hash}, AnalysisContractVersion: impact.AnalysisContractVersion, Filters: impact.Filters{RelationshipKinds: []string{"explicit"}, Direction: impact.DirectionIncoming}, Limits: impact.Limits{MaxDepth: 3, MaxNodes: 500, DefaultPathsPerTarget: 1, ExpandedMaxPaths: 20}, Suspected: impact.SuspectedOptions{MaxSeeds: 20, MaxResults: 20, GraphMaxDepth: 2}}
}

func TestFreshnessIsDerivedAtReadTimeWithoutChangingHistoricalInput(t *testing.T) {
	saved := freshnessInput(t)
	current := saved
	if result := EvaluateFreshness(saved, &current); !result.Fresh || len(result.Reasons) != 0 {
		t.Fatalf("fresh=%#v", result)
	}
	current.Base.RevisionID, _ = domain.NewID()
	current.Target.GraphManifestHash = strings.Repeat("b", 64)
	result := EvaluateFreshness(saved, &current)
	if result.Fresh || len(result.Reasons) != 2 || result.Reasons[0] != "base_revision_changed" || result.Reasons[1] != "graph_identity_changed" || saved.Base.RevisionID == current.Base.RevisionID {
		t.Fatalf("stale=%#v saved=%#v current=%#v", result, saved, current)
	}
	if unavailable := EvaluateFreshness(saved, nil); unavailable.Fresh || len(unavailable.Reasons) != 1 || unavailable.Reasons[0] != "no_current_selection" {
		t.Fatalf("unavailable=%#v", unavailable)
	}
}
