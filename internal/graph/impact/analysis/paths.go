package analysis

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/zouyi/eco-guardian/internal/graph/impact"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

type ProgressFunc func(completed, total int)

type PathOptions struct {
	Concurrency int
	Timeout     time.Duration
	Progress    ProgressFunc
}

func DefaultPaths(ctx context.Context, provider impact.GraphQueryProvider, input impact.Input, changed []impact.ChangedEntity, affected []impact.AffectedEntity, options PathOptions, requestID string) ([]impact.AffectedEntity, []impact.TruncationReason, []string, error) {
	starts := eligibleStarts(changed)
	if len(affected) == 0 || len(starts) == 0 {
		return append([]impact.AffectedEntity(nil), affected...), nil, nil, nil
	}
	concurrency := options.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	if concurrency > 16 {
		concurrency = 16
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	type result struct {
		index    int
		response impact.PathsResponse
		err      error
	}
	work := make(chan int)
	results := make(chan result, len(affected))
	var workers sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range work {
				if err := ctx.Err(); err != nil {
					results <- result{index: index, err: err}
					continue
				}
				callCtx, cancel := context.WithTimeout(ctx, timeout)
				response, err := provider.Paths(callCtx, impact.PathsRequest{Namespace: string(input.ProjectID), SnapshotVersion: string(input.Target.RevisionID), SourceNodeIDs: starts, TargetNodeIDs: []string{affected[index].Node.ID}, RelationshipKinds: []string{"explicit"}, NodeTypes: append([]string(nil), input.Filters.NodeTypes...), EdgeTypes: append([]string(nil), input.Filters.EdgeTypes...), Direction: input.Filters.Direction, MaxDepth: input.Limits.MaxDepth, MaxNodes: input.Limits.MaxNodes, MaxPaths: 1}, fmt.Sprintf("%s.%d", requestID, index+1))
				cancel()
				results <- result{index: index, response: response, err: err}
			}
		}()
	}
	go func() {
		defer close(work)
		for index := range affected {
			select {
			case <-ctx.Done():
				return
			case work <- index:
			}
		}
	}()
	go func() { workers.Wait(); close(results) }()
	ordered := append([]impact.AffectedEntity(nil), affected...)
	allReasons := []impact.TruncationReason{}
	warnings := []string{}
	completed := 0
	seenResults := 0
	for item := range results {
		seenResults++
		if item.err != nil {
			return nil, nil, nil, item.err
		}
		if err := verifyIdentity(input, item.response.ResolvedSnapshotVersion, item.response.ContentHash); err != nil {
			return nil, nil, nil, err
		}
		if len(item.response.Paths) != 1 || !validatePath(item.response.Paths[0], starts, ordered[item.index].Node.ID, input) {
			return nil, nil, nil, ErrProviderContract
		}
		path := clonePath(item.response.Paths[0])
		ordered[item.index].DefaultPath = &path
		ordered[item.index].TagRule = hasTagRule(path)
		allReasons = append(allReasons, item.response.TruncationReasons...)
		warnings = append(warnings, item.response.Warnings...)
		completed++
		if options.Progress != nil {
			options.Progress(completed, len(affected))
		}
	}
	if seenResults != len(affected) {
		return nil, nil, nil, ctx.Err()
	}
	return ordered, MergeReasons(allReasons), stableWarnings(warnings), nil
}

func ExpandPaths(ctx context.Context, provider impact.GraphQueryProvider, input impact.Input, changed []impact.ChangedEntity, targetNodeID string, maxPaths int, requestID string) ([]impact.Path, []impact.TruncationReason, []string, error) {
	if maxPaths == 0 {
		maxPaths = 20
	}
	if maxPaths < 1 || maxPaths > 100 || targetNodeID == "" {
		return nil, nil, nil, ErrProviderContract
	}
	response, err := provider.Paths(ctx, impact.PathsRequest{Namespace: string(input.ProjectID), SnapshotVersion: string(input.Target.RevisionID), SourceNodeIDs: eligibleStarts(changed), TargetNodeIDs: []string{targetNodeID}, RelationshipKinds: []string{"explicit"}, NodeTypes: append([]string(nil), input.Filters.NodeTypes...), EdgeTypes: append([]string(nil), input.Filters.EdgeTypes...), Direction: input.Filters.Direction, MaxDepth: input.Limits.MaxDepth, MaxNodes: input.Limits.MaxNodes, MaxPaths: maxPaths}, requestID)
	if err != nil {
		return nil, nil, nil, err
	}
	if err = verifyIdentity(input, response.ResolvedSnapshotVersion, response.ContentHash); err != nil {
		return nil, nil, nil, err
	}
	paths := make([]impact.Path, len(response.Paths))
	for index, path := range response.Paths {
		if !validatePath(path, eligibleStarts(changed), targetNodeID, input) {
			return nil, nil, nil, ErrProviderContract
		}
		paths[index] = clonePath(path)
	}
	return paths, MergeReasons(response.TruncationReasons), stableWarnings(response.Warnings), nil
}

func validatePath(path impact.Path, starts []string, target string, input impact.Input) bool {
	if path.TargetNodeID != target || path.HopCount < 1 || path.HopCount > input.Limits.MaxDepth || len(path.NodeIDs) != path.HopCount+1 || len(path.EdgeIDs) != path.HopCount || len(path.Nodes) != len(path.NodeIDs) || len(path.Edges) != len(path.EdgeIDs) || path.NodeIDs[0] != path.SourceNodeID || path.NodeIDs[len(path.NodeIDs)-1] != target || !contains(starts, path.SourceNodeID) {
		return false
	}
	seen := map[string]struct{}{}
	for index, nodeID := range path.NodeIDs {
		if nodeID == "" || path.Nodes[index].ID != nodeID {
			return false
		}
		if _, duplicate := seen[nodeID]; duplicate {
			return false
		}
		seen[nodeID] = struct{}{}
	}
	for index, edgeID := range path.EdgeIDs {
		edge := path.Edges[index]
		if edge.ID != edgeID || edge.RelationKind != "explicit" || edge.Provenance == nil || !edgeAllowed(edge, path.NodeIDs[index], path.NodeIDs[index+1], input.Filters.Direction) || !containsOptional(input.Filters.EdgeTypes, edge.Type) || !containsOptional(input.Filters.NodeTypes, path.Nodes[index+1].Type) {
			return false
		}
	}
	return true
}

func edgeAllowed(edge graphsync.Edge, from, to string, direction impact.Direction) bool {
	switch direction {
	case impact.DirectionIncoming:
		return edge.From == to && edge.To == from
	case impact.DirectionOutgoing:
		return edge.From == from && edge.To == to
	case impact.DirectionBoth:
		return (edge.From == from && edge.To == to) || (edge.From == to && edge.To == from)
	default:
		return false
	}
}

func clonePath(path impact.Path) impact.Path {
	copy := path
	copy.NodeIDs = append([]string(nil), path.NodeIDs...)
	copy.EdgeIDs = append([]string(nil), path.EdgeIDs...)
	copy.Nodes = append(copy.Nodes[:0:0], path.Nodes...)
	copy.Edges = append(copy.Edges[:0:0], path.Edges...)
	copy.Reasons = append([]impact.TruncationReason(nil), path.Reasons...)
	return copy
}

func hasTagRule(path impact.Path) bool {
	for _, edge := range path.Edges {
		if edge.Type == "entity_has_tag" || edge.Type == "item_enhances_tag" {
			return true
		}
	}
	return false
}

func contains(values []string, value string) bool {
	index := sort.SearchStrings(values, value)
	return index < len(values) && values[index] == value
}

func containsOptional(values []string, value string) bool {
	return len(values) == 0 || contains(values, value)
}

func stableWarnings(values []string) []string {
	set := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := set[value]; exists {
			continue
		}
		set[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
