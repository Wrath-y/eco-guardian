package retrieval

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/graph/impact"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

const MaxQueryBytes = 16 * 1024

func BuildQuery(changed []impact.ChangedEntity, nodes map[string]graphsync.Node) string {
	rows := append([]impact.ChangedEntity(nil), changed...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].EntityID < rows[j].EntityID })
	var builder strings.Builder
	builder.WriteString("dependency-impact-query-v1\n")
	for _, item := range rows {
		if !item.QueryEligible || item.TargetNodeID == "" {
			continue
		}
		node := nodes[item.TargetNodeID]
		paths := append([]string(nil), item.FieldPaths...)
		sort.Strings(paths)
		line := fmt.Sprintf("node=%s kind=%s change=%s fields=%s label=%s text=%s\n", item.TargetNodeID, item.Kind, item.ChangeKind, strings.Join(paths, ","), node.Label, node.Text)
		remaining := MaxQueryBytes - builder.Len()
		if remaining <= 0 {
			break
		}
		if len(line) > remaining {
			line = line[:remaining]
		}
		builder.WriteString(line)
	}
	return builder.String()
}

type Result struct {
	State    impact.SuspectedState
	Evidence []impact.SuspectedEvidence
	Warnings []string
}

func Retrieve(ctx context.Context, provider impact.GraphQueryProvider, input impact.Input, changed []impact.ChangedEntity, nodes map[string]graphsync.Node, requestID string) (Result, error) {
	if !input.Suspected.Enabled {
		return Result{State: impact.SuspectedDisabled}, nil
	}
	query := BuildQuery(changed, nodes)
	response, err := provider.Retrieve(ctx, impact.RetrieveRequest{Namespace: string(input.ProjectID), SnapshotVersion: string(input.Target.RevisionID), Query: query, RelationshipKinds: []string{"explicit"}, NodeTypes: append([]string(nil), input.Filters.NodeTypes...), EdgeTypes: append([]string(nil), input.Filters.EdgeTypes...), MaxSeeds: input.Suspected.MaxSeeds, MaxResults: input.Suspected.MaxResults, GraphMaxDepth: input.Suspected.GraphMaxDepth}, requestID)
	if err != nil {
		var providerError *graphsync.ProviderError
		if !asProviderError(err, &providerError) {
			return Result{State: impact.SuspectedUnavailable, Warnings: []string{"RETRIEVAL_UNAVAILABLE"}}, nil
		}
		switch providerError.Code {
		case "SNAPSHOT_INDEX_NOT_READY":
			return Result{State: impact.SuspectedRebuildRequired, Warnings: []string{"SNAPSHOT_INDEX_NOT_READY"}}, nil
		case "RETRIEVAL_UNAVAILABLE", "GRAPH_STORE_UNAVAILABLE":
			return Result{State: impact.SuspectedUnavailable, Warnings: []string{providerError.Code}}, nil
		default:
			return Result{}, err
		}
	}
	if response.ResolvedSnapshotVersion != string(input.Target.RevisionID) || response.ContentHash != input.Target.GraphManifestHash {
		return Result{}, fmt.Errorf("PROVIDER_CONTRACT_MISMATCH")
	}
	state := impact.SuspectedReady
	if response.Degraded {
		state = impact.SuspectedDegraded
	}
	evidence := append([]impact.SuspectedEvidence(nil), response.Results...)
	return Result{State: state, Evidence: evidence, Warnings: stable(response.Warnings)}, nil
}

func asProviderError(err error, target **graphsync.ProviderError) bool {
	provider, ok := err.(*graphsync.ProviderError)
	if ok {
		*target = provider
	}
	return ok
}

func stable(values []string) []string {
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
