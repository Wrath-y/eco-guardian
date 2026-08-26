package analysis

import (
	"context"
	"errors"
	"sort"

	"github.com/zouyi/eco-guardian/internal/graph/impact"
)

var (
	ErrProviderContract = errors.New("PROVIDER_CONTRACT_MISMATCH")
	ErrNodeDrift        = errors.New("NODE_NOT_FOUND")
)

type DeterministicResult struct {
	Affected  []impact.AffectedEntity
	Reasons   []impact.TruncationReason
	Warnings  []string
	Truncated bool
	State     string
}

func Traverse(ctx context.Context, provider impact.GraphQueryProvider, input impact.Input, changed []impact.ChangedEntity, requestID string) (DeterministicResult, error) {
	starts := eligibleStarts(changed)
	if len(starts) == 0 {
		return DeterministicResult{State: "no_eligible_starts"}, nil
	}
	response, err := provider.Traverse(ctx, impact.TraverseRequest{Namespace: string(input.ProjectID), SnapshotVersion: string(input.Target.RevisionID), StartNodeIDs: starts, RelationshipKinds: []string{"explicit"}, NodeTypes: append([]string(nil), input.Filters.NodeTypes...), EdgeTypes: append([]string(nil), input.Filters.EdgeTypes...), Direction: input.Filters.Direction, MaxDepth: input.Limits.MaxDepth, MaxNodes: input.Limits.MaxNodes}, requestID)
	if err != nil {
		return DeterministicResult{}, err
	}
	if err = verifyIdentity(input, response.ResolvedSnapshotVersion, response.ContentHash); err != nil {
		return DeterministicResult{}, err
	}
	startSet := make(map[string]struct{}, len(starts))
	for _, id := range starts {
		startSet[id] = struct{}{}
	}
	byID := map[string]impact.AffectedEntity{}
	for _, record := range response.Nodes {
		if record.Node.ID == "" || record.Depth < 0 || record.Depth > input.Limits.MaxDepth {
			return DeterministicResult{}, ErrProviderContract
		}
		if record.Depth == 0 {
			if _, start := startSet[record.Node.ID]; !start {
				return DeterministicResult{}, ErrProviderContract
			}
			continue
		}
		if _, start := startSet[record.Node.ID]; start {
			continue
		}
		existing, found := byID[record.Node.ID]
		if !found || record.Depth < existing.MinimumDepth {
			byID[record.Node.ID] = impact.AffectedEntity{Node: record.Node, MinimumDepth: record.Depth, Direct: record.Depth == 1, Indirect: record.Depth >= 2}
		}
	}
	affected := make([]impact.AffectedEntity, 0, len(byID))
	for _, item := range byID {
		affected = append(affected, item)
	}
	sort.Slice(affected, func(i, j int) bool {
		if affected[i].MinimumDepth != affected[j].MinimumDepth {
			return affected[i].MinimumDepth < affected[j].MinimumDepth
		}
		return affected[i].Node.ID < affected[j].Node.ID
	})
	state := "empty"
	if len(affected) > 0 {
		state = "ready"
	}
	reasons := MergeReasons(response.TruncationReasons)
	return DeterministicResult{Affected: affected, Reasons: reasons, Warnings: append([]string(nil), response.Warnings...), Truncated: response.Truncated, State: state}, nil
}

func eligibleStarts(changed []impact.ChangedEntity) []string {
	set := map[string]struct{}{}
	for _, entity := range changed {
		if entity.QueryEligible && entity.TargetNodeID != "" {
			set[entity.TargetNodeID] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

func verifyIdentity(input impact.Input, version, hash string) error {
	if version != string(input.Target.RevisionID) || hash != input.Target.GraphManifestHash {
		return ErrProviderContract
	}
	return nil
}

func MergeReasons(groups ...[]impact.TruncationReason) []impact.TruncationReason {
	seen := map[impact.TruncationReason]bool{}
	for _, group := range groups {
		for _, reason := range group {
			if reason == impact.MaxDepth || reason == impact.MaxNodes || reason == impact.MaxPaths {
				seen[reason] = true
			}
		}
	}
	result := make([]impact.TruncationReason, 0, 3)
	for _, reason := range []impact.TruncationReason{impact.MaxDepth, impact.MaxNodes, impact.MaxPaths} {
		if seen[reason] {
			result = append(result, reason)
		}
	}
	return result
}

func WarningFor(reason impact.TruncationReason) string {
	switch reason {
	case impact.MaxDepth:
		return "MAX_DEPTH: narrow relationship/type filters or increase depth within the maximum of 6"
	case impact.MaxNodes:
		return "MAX_NODES: narrow node/relation filters; the report contains a deterministic prefix"
	case impact.MaxPaths:
		return "MAX_PATHS: narrow path filters or increase max_paths within the maximum of 100"
	default:
		return ""
	}
}
