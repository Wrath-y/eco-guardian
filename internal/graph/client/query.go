package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/zouyi/eco-guardian/internal/graph/impact"
)

func (c *Client) Traverse(ctx context.Context, request impact.TraverseRequest, requestID string) (impact.TraverseResponse, error) {
	if err := validateTraverse(request); err != nil {
		return impact.TraverseResponse{}, err
	}
	var response impact.TraverseResponse
	if err := c.call(ctx, http.MethodPost, queryPath(request.Namespace, request.SnapshotVersion, "traverse"), requestID, request, &response, http.StatusOK); err != nil {
		return impact.TraverseResponse{}, err
	}
	if err := validateTraverseResponse(request, response); err != nil {
		return impact.TraverseResponse{}, err
	}
	return response, nil
}

func (c *Client) Paths(ctx context.Context, request impact.PathsRequest, requestID string) (impact.PathsResponse, error) {
	if err := validatePaths(request); err != nil {
		return impact.PathsResponse{}, err
	}
	var response impact.PathsResponse
	if err := c.call(ctx, http.MethodPost, queryPath(request.Namespace, request.SnapshotVersion, "paths"), requestID, request, &response, http.StatusOK); err != nil {
		return impact.PathsResponse{}, err
	}
	if err := validatePathsResponse(request, response); err != nil {
		return impact.PathsResponse{}, err
	}
	return response, nil
}

func (c *Client) Retrieve(ctx context.Context, request impact.RetrieveRequest, requestID string) (impact.RetrieveResponse, error) {
	if err := validateRetrieve(request); err != nil {
		return impact.RetrieveResponse{}, err
	}
	var response impact.RetrieveResponse
	if err := c.call(ctx, http.MethodPost, queryPath(request.Namespace, request.SnapshotVersion, "retrieve"), requestID, request, &response, http.StatusOK); err != nil {
		return impact.RetrieveResponse{}, err
	}
	if err := validateRetrieveResponse(request, response); err != nil {
		return impact.RetrieveResponse{}, err
	}
	return response, nil
}

func queryPath(namespace, version, operation string) string {
	return "/v1/graphs/" + url.PathEscape(namespace) + "/snapshots/" + url.PathEscape(version) + "/" + operation
}

type ImpactQueryProvider struct {
	Client *Client
	Retry  RetryPolicy
}

func (p ImpactQueryProvider) Traverse(ctx context.Context, request impact.TraverseRequest, requestID string) (response impact.TraverseResponse, err error) {
	if p.Client == nil {
		return response, fmt.Errorf("%w: query client is required", ErrContract)
	}
	err = p.Retry.DoWithCorrelation(ctx, requestID, func(ctx context.Context, attempt RetryAttempt) error {
		var callErr error
		response, callErr = p.Client.Traverse(ctx, request, attempt.RootRequestID)
		return callErr
	})
	return response, err
}

func (p ImpactQueryProvider) Paths(ctx context.Context, request impact.PathsRequest, requestID string) (response impact.PathsResponse, err error) {
	if p.Client == nil {
		return response, fmt.Errorf("%w: query client is required", ErrContract)
	}
	err = p.Retry.DoWithCorrelation(ctx, requestID, func(ctx context.Context, attempt RetryAttempt) error {
		var callErr error
		response, callErr = p.Client.Paths(ctx, request, attempt.RootRequestID)
		return callErr
	})
	return response, err
}

func (p ImpactQueryProvider) Retrieve(ctx context.Context, request impact.RetrieveRequest, requestID string) (response impact.RetrieveResponse, err error) {
	if p.Client == nil {
		return response, fmt.Errorf("%w: query client is required", ErrContract)
	}
	err = p.Retry.DoWithCorrelation(ctx, requestID, func(ctx context.Context, attempt RetryAttempt) error {
		var callErr error
		response, callErr = p.Client.Retrieve(ctx, request, attempt.RootRequestID)
		return callErr
	})
	return response, err
}

var _ impact.GraphQueryProvider = ImpactQueryProvider{}

func validateTraverse(request impact.TraverseRequest) error {
	if !validQueryIdentity(request.Namespace, request.SnapshotVersion) || len(request.StartNodeIDs) == 0 || !uniqueNonempty(request.StartNodeIDs) || !validExplicit(request.RelationshipKinds) || !request.Direction.Valid() || request.MaxDepth < 1 || request.MaxDepth > 6 || request.MaxNodes < 1 || request.MaxNodes > 500 || !uniqueNonemptyOptional(request.NodeTypes) || !uniqueNonemptyOptional(request.EdgeTypes) {
		return fmt.Errorf("%w: invalid traverse request", ErrContract)
	}
	return nil
}

func validatePaths(request impact.PathsRequest) error {
	if !validQueryIdentity(request.Namespace, request.SnapshotVersion) || len(request.SourceNodeIDs) == 0 || len(request.TargetNodeIDs) == 0 || !uniqueNonempty(request.SourceNodeIDs) || !uniqueNonempty(request.TargetNodeIDs) || !validExplicit(request.RelationshipKinds) || !request.Direction.Valid() || request.MaxDepth < 1 || request.MaxDepth > 6 || request.MaxNodes < 1 || request.MaxNodes > 500 || request.MaxPaths < 1 || request.MaxPaths > 100 || !uniqueNonemptyOptional(request.NodeTypes) || !uniqueNonemptyOptional(request.EdgeTypes) {
		return fmt.Errorf("%w: invalid paths request", ErrContract)
	}
	return nil
}

func validateRetrieve(request impact.RetrieveRequest) error {
	if !validQueryIdentity(request.Namespace, request.SnapshotVersion) || strings.TrimSpace(request.Query) == "" || len(request.Query) > 16*1024 || !validExplicit(request.RelationshipKinds) || request.MaxSeeds < 1 || request.MaxSeeds > 100 || request.MaxResults < 1 || request.MaxResults > 100 || request.GraphMaxDepth < 0 || request.GraphMaxDepth > 3 || !uniqueNonemptyOptional(request.NodeTypes) || !uniqueNonemptyOptional(request.EdgeTypes) {
		return fmt.Errorf("%w: invalid retrieve request", ErrContract)
	}
	return nil
}

func validateTraverseResponse(request impact.TraverseRequest, response impact.TraverseResponse) error {
	if !validResponseIdentity(request.SnapshotVersion, response.ResolvedSnapshotVersion, response.ContentHash) || !validReasons(response.Truncated, response.TruncationReasons) {
		return fmt.Errorf("%w: invalid traverse response", ErrContract)
	}
	seen := map[string]struct{}{}
	for _, record := range response.Nodes {
		if record.Node.ID == "" || record.Depth < 0 || record.Depth > request.MaxDepth {
			return fmt.Errorf("%w: invalid traverse node", ErrContract)
		}
		if _, duplicate := seen[record.Node.ID]; duplicate {
			return fmt.Errorf("%w: duplicate traverse node", ErrContract)
		}
		seen[record.Node.ID] = struct{}{}
	}
	return nil
}

func validatePathsResponse(request impact.PathsRequest, response impact.PathsResponse) error {
	if !validResponseIdentity(request.SnapshotVersion, response.ResolvedSnapshotVersion, response.ContentHash) || len(response.Paths) > request.MaxPaths || !validReasons(response.Truncated, response.TruncationReasons) {
		return fmt.Errorf("%w: invalid paths response", ErrContract)
	}
	for _, path := range response.Paths {
		if !validPath(path, request.MaxDepth) {
			return fmt.Errorf("%w: invalid path response", ErrContract)
		}
	}
	return nil
}

func validateRetrieveResponse(request impact.RetrieveRequest, response impact.RetrieveResponse) error {
	if !validResponseIdentity(request.SnapshotVersion, response.ResolvedSnapshotVersion, response.ContentHash) || response.Mode == "" || len(response.Results) > request.MaxResults {
		return fmt.Errorf("%w: invalid retrieve response", ErrContract)
	}
	for index, result := range response.Results {
		if result.Rank != index+1 || result.Node.ID == "" || strings.TrimSpace(result.CitationText) == "" || result.AlgorithmVersion == "" || result.SeedNodeID == "" || (result.Path != nil && !validPath(*result.Path, request.GraphMaxDepth)) {
			return fmt.Errorf("%w: invalid retrieve evidence", ErrContract)
		}
	}
	return nil
}

func validPath(path impact.Path, maxDepth int) bool {
	if path.SourceNodeID == "" || path.TargetNodeID == "" || path.HopCount < 0 || path.HopCount > maxDepth || path.HopCount != len(path.EdgeIDs) || len(path.NodeIDs) != len(path.EdgeIDs)+1 || len(path.Nodes) != len(path.NodeIDs) || len(path.Edges) != len(path.EdgeIDs) || path.NodeIDs[0] != path.SourceNodeID || path.NodeIDs[len(path.NodeIDs)-1] != path.TargetNodeID {
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
		if edgeID == "" || path.Edges[index].ID != edgeID || path.Edges[index].RelationKind != "explicit" || path.Edges[index].Provenance == nil {
			return false
		}
	}
	return validReasons(path.Truncated, path.Reasons)
}

func validQueryIdentity(namespace, version string) bool {
	return strings.TrimSpace(namespace) != "" && strings.TrimSpace(version) != ""
}
func validResponseIdentity(want, got, hash string) bool { return want == got && impact.ValidHash(hash) }
func validExplicit(values []string) bool                { return len(values) == 1 && values[0] == "explicit" }

func uniqueNonempty(values []string) bool { return len(values) > 0 && uniqueNonemptyOptional(values) }
func uniqueNonemptyOptional(values []string) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validReasons(truncated bool, reasons []impact.TruncationReason) bool {
	if !truncated && len(reasons) != 0 {
		return false
	}
	order := map[impact.TruncationReason]int{impact.MaxDepth: 1, impact.MaxNodes: 2, impact.MaxPaths: 3}
	previous := 0
	for _, reason := range reasons {
		current := order[reason]
		if current == 0 || current <= previous {
			return false
		}
		previous = current
	}
	return true
}
