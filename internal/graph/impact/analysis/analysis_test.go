package analysis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

type graphFake struct {
	traverse impact.TraverseResponse
	paths    map[string]impact.PathsResponse
	err      error
	mu       sync.Mutex
	calls    []string
}

type capacityGraphFake struct {
	request impact.TraverseRequest
	result  impact.TraverseResponse
}

func (f *capacityGraphFake) Traverse(_ context.Context, request impact.TraverseRequest, _ string) (impact.TraverseResponse, error) {
	f.request = request
	return f.result, nil
}
func (*capacityGraphFake) Paths(context.Context, impact.PathsRequest, string) (impact.PathsResponse, error) {
	return impact.PathsResponse{}, nil
}
func (*capacityGraphFake) Retrieve(context.Context, impact.RetrieveRequest, string) (impact.RetrieveResponse, error) {
	return impact.RetrieveResponse{}, nil
}

func (f *graphFake) Traverse(context.Context, impact.TraverseRequest, string) (impact.TraverseResponse, error) {
	return f.traverse, f.err
}
func (f *graphFake) Paths(_ context.Context, request impact.PathsRequest, _ string) (impact.PathsResponse, error) {
	f.mu.Lock()
	f.calls = append(f.calls, request.TargetNodeIDs[0])
	f.mu.Unlock()
	return f.paths[request.TargetNodeIDs[0]], f.err
}
func (f *graphFake) Retrieve(context.Context, impact.RetrieveRequest, string) (impact.RetrieveResponse, error) {
	return impact.RetrieveResponse{}, f.err
}

func impactInput(t *testing.T) impact.Input {
	t.Helper()
	project, _ := domain.NewID()
	base, _ := domain.NewID()
	target, _ := domain.NewID()
	hash := strings.Repeat("a", 64)
	return impact.Input{ProjectID: project, Base: impact.RevisionIdentity{RevisionID: base, ConfigHash: hash, VersionManifestHash: hash, GraphManifestHash: hash}, Target: impact.RevisionIdentity{RevisionID: target, ConfigHash: hash, VersionManifestHash: hash, GraphManifestHash: hash}, AnalysisContractVersion: impact.AnalysisContractVersion, Filters: impact.Filters{RelationshipKinds: []string{"explicit"}, Direction: impact.DirectionIncoming}, Limits: impact.Limits{MaxDepth: 3, MaxNodes: 500, DefaultPathsPerTarget: 1, ExpandedMaxPaths: 20}, Suspected: impact.SuspectedOptions{MaxSeeds: 20, MaxResults: 20, GraphMaxDepth: 2}}
}

func node(id, kind string) graphsync.Node {
	return graphsync.Node{ID: id, Type: kind, Label: id, Text: id, Properties: map[string]any{}, Provenance: map[string]any{"entity_id": id}}
}

func TestTraverseDeduplicatesStartsCyclesAndSortsDepthThenID(t *testing.T) {
	input := impactInput(t)
	changed := []impact.ChangedEntity{{TargetNodeID: "z", QueryEligible: true}, {TargetNodeID: "a", QueryEligible: true}, {TargetNodeID: "z", QueryEligible: true}}
	provider := &graphFake{traverse: impact.TraverseResponse{ResolvedSnapshotVersion: string(input.Target.RevisionID), ContentHash: input.Target.GraphManifestHash, Nodes: []impact.NodeDepth{{Node: node("b", "effect"), Depth: 2}, {Node: node("z", "skill"), Depth: 0}, {Node: node("c", "item"), Depth: 1}, {Node: node("b", "effect"), Depth: 1}, {Node: node("a", "skill"), Depth: 3}}, Truncated: true, TruncationReasons: []impact.TruncationReason{impact.MaxDepth}}}
	result, err := Traverse(context.Background(), provider, input, changed, "request")
	if err != nil || len(result.Affected) != 2 || result.Affected[0].Node.ID != "b" || result.Affected[0].MinimumDepth != 1 || result.Affected[1].Node.ID != "c" || !result.Truncated || result.Reasons[0] != impact.MaxDepth {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestTraverseRejectsSnapshotHashMismatch(t *testing.T) {
	input := impactInput(t)
	provider := &graphFake{traverse: impact.TraverseResponse{ResolvedSnapshotVersion: string(input.Target.RevisionID), ContentHash: strings.Repeat("b", 64)}}
	_, err := Traverse(context.Background(), provider, input, []impact.ChangedEntity{{TargetNodeID: "a", QueryEligible: true}}, "request")
	if !errors.Is(err, ErrProviderContract) {
		t.Fatalf("error=%v", err)
	}
}

func TestCapacityIdentityRemainsBoundedForTenThousandNodesAndHundredThousandEdges(t *testing.T) {
	input := impactInput(t)
	input.Target.GraphNodeCount, input.Target.GraphEdgeCount = 10_000, 100_000
	input.Limits.MaxDepth, input.Limits.MaxNodes = 3, 500
	nodes := make([]impact.NodeDepth, 500)
	nodes[0] = impact.NodeDepth{Node: node("changed", "skill"), Depth: 0}
	for index := 1; index < len(nodes); index++ {
		nodes[index] = impact.NodeDepth{Node: node(fmt.Sprintf("node-%04d", index), "item"), Depth: 1 + index%3}
	}
	provider := &capacityGraphFake{result: impact.TraverseResponse{ResolvedSnapshotVersion: string(input.Target.RevisionID), ContentHash: input.Target.GraphManifestHash, Nodes: nodes, Truncated: true, TruncationReasons: []impact.TruncationReason{impact.MaxNodes}}}
	started := time.Now()
	result, err := Traverse(context.Background(), provider, input, []impact.ChangedEntity{{TargetNodeID: "changed", QueryEligible: true}}, "capacity")
	if err != nil || provider.request.MaxDepth != 3 || provider.request.MaxNodes != 500 || len(result.Affected) != 499 || !result.Truncated || result.Reasons[0] != impact.MaxNodes || time.Since(started) > 2*time.Second {
		t.Fatalf("request=%#v affected=%d truncated=%v reasons=%v elapsed=%s err=%v", provider.request, len(result.Affected), result.Truncated, result.Reasons, time.Since(started), err)
	}
}

func TestDefaultPathsRemainInAffectedOrderAndClassifyOnlyPathEvidence(t *testing.T) {
	input := impactInput(t)
	changed := []impact.ChangedEntity{{TargetNodeID: "changed", QueryEligible: true}}
	affected := []impact.AffectedEntity{{Node: node("first", "skill"), MinimumDepth: 1}, {Node: node("second", "item"), MinimumDepth: 2}}
	provider := &graphFake{paths: map[string]impact.PathsResponse{}}
	for _, target := range []string{"first", "second"} {
		edgeType := "character_has_skill"
		if target == "second" {
			edgeType = "entity_has_tag"
		}
		path := impact.Path{SourceNodeID: "changed", TargetNodeID: target, NodeIDs: []string{"changed", target}, EdgeIDs: []string{"edge-" + target}, Nodes: []graphsync.Node{node("changed", "attribute"), node(target, affected[0].Node.Type)}, Edges: []graphsync.Edge{{ID: "edge-" + target, From: target, To: "changed", Type: edgeType, RelationKind: "explicit", Confidence: 1, Properties: map[string]any{}, Provenance: map[string]any{"field_path": "/payload/x"}}}, HopCount: 1}
		provider.paths[target] = impact.PathsResponse{ResolvedSnapshotVersion: string(input.Target.RevisionID), ContentHash: input.Target.GraphManifestHash, Paths: []impact.Path{path}}
	}
	var progress []int
	result, _, _, err := DefaultPaths(context.Background(), provider, input, changed, affected, PathOptions{Concurrency: 2, Timeout: time.Second, Progress: func(completed, _ int) { progress = append(progress, completed) }}, "request")
	if err != nil || len(result) != 2 || result[0].Node.ID != "first" || result[1].Node.ID != "second" || result[0].TagRule || !result[1].TagRule || result[0].DefaultPath.TargetNodeID != "first" || len(progress) != 2 {
		t.Fatalf("result=%#v progress=%v err=%v", result, progress, err)
	}
}

func TestDefaultPathRejectsWrongStoredOrientation(t *testing.T) {
	input := impactInput(t)
	path := impact.Path{SourceNodeID: "changed", TargetNodeID: "target", NodeIDs: []string{"changed", "target"}, EdgeIDs: []string{"edge"}, Nodes: []graphsync.Node{node("changed", "attribute"), node("target", "skill")}, Edges: []graphsync.Edge{{ID: "edge", From: "changed", To: "target", Type: "character_has_skill", RelationKind: "explicit", Provenance: map[string]any{}}}, HopCount: 1}
	provider := &graphFake{paths: map[string]impact.PathsResponse{"target": {ResolvedSnapshotVersion: string(input.Target.RevisionID), ContentHash: input.Target.GraphManifestHash, Paths: []impact.Path{path}}}}
	_, _, _, err := DefaultPaths(context.Background(), provider, input, []impact.ChangedEntity{{TargetNodeID: "changed", QueryEligible: true}}, []impact.AffectedEntity{{Node: node("target", "skill"), MinimumDepth: 1}}, PathOptions{}, "request")
	if !errors.Is(err, ErrProviderContract) {
		t.Fatalf("error=%v", err)
	}
}

func TestMergeReasonsHasFixedOrder(t *testing.T) {
	result := MergeReasons([]impact.TruncationReason{impact.MaxPaths, impact.MaxDepth}, []impact.TruncationReason{impact.MaxNodes, impact.MaxPaths})
	if len(result) != 3 || result[0] != impact.MaxDepth || result[1] != impact.MaxNodes || result[2] != impact.MaxPaths {
		t.Fatalf("reasons=%v", result)
	}
}

func TestAllV1RelationsPreserveExplicitEvidenceAndTagClassification(t *testing.T) {
	input := impactInput(t)
	for _, relation := range projector.V1Relations() {
		path := impact.Path{SourceNodeID: "changed", TargetNodeID: "affected", NodeIDs: []string{"changed", "affected"}, EdgeIDs: []string{"edge"}, Nodes: []graphsync.Node{node("changed", "attribute"), node("affected", "skill")}, Edges: []graphsync.Edge{{ID: "edge", From: "affected", To: "changed", Type: string(relation.Token), RelationKind: "explicit", Confidence: 1, Properties: map[string]any{}, Provenance: map[string]any{"field_path": relation.PathPattern, "ordinal": 0}}}, HopCount: 1}
		if !validatePath(path, []string{"changed"}, "affected", input) {
			t.Fatalf("relation %s rejected", relation.Token)
		}
		wantTag := relation.Token == projector.EntityHasTag || relation.Token == projector.ItemEnhancesTag
		if hasTagRule(path) != wantTag {
			t.Fatalf("relation %s tag=%v", relation.Token, hasTagRule(path))
		}
	}
}

func TestDisplayTextAndProviderAvailabilityCannotAffectAffectedIdentityOrder(t *testing.T) {
	input := impactInput(t)
	changed := []impact.ChangedEntity{{TargetNodeID: "changed", QueryEligible: true}}
	makeResponse := func(prefix string) impact.TraverseResponse {
		first, second := node("a", "skill"), node("b", "item")
		first.Label, first.Text, second.Label, second.Text = prefix+" first", prefix+" text", prefix+" second", prefix+" other"
		return impact.TraverseResponse{ResolvedSnapshotVersion: string(input.Target.RevisionID), ContentHash: input.Target.GraphManifestHash, Nodes: []impact.NodeDepth{{Node: second, Depth: 2}, {Node: first, Depth: 1}}}
	}
	a, err := Traverse(context.Background(), &graphFake{traverse: makeResponse("healthy-models")}, input, changed, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Traverse(context.Background(), &graphFake{traverse: makeResponse("all-models-offline")}, input, changed, "b")
	if err != nil || len(a.Affected) != len(b.Affected) {
		t.Fatalf("a=%#v b=%#v err=%v", a, b, err)
	}
	for index := range a.Affected {
		if a.Affected[index].Node.ID != b.Affected[index].Node.ID || a.Affected[index].MinimumDepth != b.Affected[index].MinimumDepth {
			t.Fatalf("identities differ a=%#v b=%#v", a.Affected, b.Affected)
		}
	}
}
