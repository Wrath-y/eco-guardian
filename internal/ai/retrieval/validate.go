package retrieval

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
)

var (
	ErrSnapshotIdentityMismatch = errors.New("retrieval Snapshot identity mismatch")
	ErrResponseInvalid          = errors.New("retrieval response is invalid")
	ErrResponseFilterMismatch   = errors.New("retrieval response filter mismatch")
)

// ValidateResponse is the exposure gate for provider data. Callers must not
// persist, cite or pass a Response to a Provider until this succeeds.
func ValidateResponse(request Request, response Response) error {
	if !request.Valid() || response.ResolvedSnapshotVersion != request.Base.GraphSnapshot || response.ContentHash != string(request.Base.GraphContentHash) {
		return ErrSnapshotIdentityMismatch
	}
	if !validModeGenerations(response) || !validWarnings(response.Warnings) || len(response.Results) > request.Budget.RetrievalResultLimit {
		return ErrResponseInvalid
	}
	nodeTypes := stringSet(request.Filters.NodeTypes)
	edgeTypes := stringSet(request.Filters.EdgeTypes)
	for index, result := range response.Results {
		if result.Rank != index+1 || !validNode(result.Node) || result.HopCount < 0 || result.HopCount > request.Budget.RetrievalGraphDepth || !unitNumber(result.PathConfidence) || !validScores(result.Scores) || result.Evidence.Seed == nil || result.Evidence.Path == nil || result.Evidence.Seed.NodeID == "" || result.Evidence.Seed.SearchText == "" {
			return ErrResponseInvalid
		}
		if !matchesFilter(nodeTypes, result.Node.Type) {
			return ErrResponseFilterMismatch
		}
		if !validPath(result.Evidence.Path, nodeTypes, edgeTypes, request.Budget.RetrievalGraphDepth) {
			return ErrResponseFilterMismatch
		}
	}
	return nil
}

func validModeGenerations(response Response) bool {
	if response.Rerank != "used" && response.Rerank != "unavailable" && response.Rerank != "transient_failure" && response.Rerank != "permanent_failure" && response.Rerank != "index_evicted" && response.Rerank != "skipped" {
		return false
	}
	ftsValid := response.FTSGeneration != nil && validGeneration(*response.FTSGeneration, "fts")
	vectorValid := response.VectorGeneration != nil && validGeneration(*response.VectorGeneration, "vector")
	switch response.ModeUsed {
	case "hybrid":
		return ftsValid && vectorValid
	case "bm25_only":
		return response.Degraded && ftsValid && response.VectorGeneration == nil
	case "vector_only":
		return response.Degraded && vectorValid && response.FTSGeneration == nil
	default:
		return false
	}
}

func validGeneration(value Generation, component string) bool {
	if value.Component != component || value.Generation == "" || value.Algorithm == "" || !hash(value.ContentDigest) {
		return false
	}
	if component == "fts" {
		return value.Tokenizer != "" && value.Provider == "" && value.Model == "" && value.Dimensions == 0
	}
	return value.Provider != "" && value.Model != "" && value.Dimensions > 0 && value.Tokenizer == ""
}

func validWarnings(values []Warning) bool {
	for _, warning := range values {
		if (warning.Stage != "bm25" && warning.Stage != "vector" && warning.Stage != "rerank") || warning.Code == "" || warning.Message == "" {
			return false
		}
	}
	return true
}

func validPath(path *PathEvidence, nodeTypes, edgeTypes map[string]struct{}, maxDepth int) bool {
	if path == nil || len(path.NodeIDs) == 0 || len(path.EdgeIDs) > maxDepth || len(path.NodeIDs) != len(path.EdgeIDs)+1 || len(path.Nodes) != len(path.NodeIDs) || len(path.Edges) != len(path.EdgeIDs) || len(path.InferredEdges) != 0 || len(path.ExplicitEdges) != len(path.Edges) {
		return false
	}
	for _, node := range path.Nodes {
		if !validNode(node) || !matchesFilter(nodeTypes, node.Type) {
			return false
		}
	}
	for _, edge := range path.Edges {
		if !validEdge(edge) || edge.RelationKind != "explicit" || !matchesFilter(edgeTypes, edge.Type) {
			return false
		}
	}
	for _, edge := range path.ExplicitEdges {
		if !validEdge(edge) || edge.RelationKind != "explicit" || !matchesFilter(edgeTypes, edge.Type) {
			return false
		}
	}
	return true
}

func validNode(value Node) bool {
	return value.ID != "" && value.Type != "" && value.Label != "" && validObject(value.Properties) && validObject(value.Provenance)
}

func validEdge(value Edge) bool {
	return value.ID != "" && value.From != "" && value.To != "" && value.Type != "" && unitNumber(value.Confidence) && validObject(value.Properties) && validObject(value.Provenance)
}

func validScores(value Scores) bool {
	if !finiteNumber(value.RRFScore) || !finiteNumber(value.GraphScore) || (value.BM25Rank != nil && *value.BM25Rank < 1) || (value.VectorRank != nil && *value.VectorRank < 1) {
		return false
	}
	for _, score := range []*json.Number{value.BM25Score, value.VectorScore, value.RerankScore} {
		if score != nil && !finiteNumber(*score) {
			return false
		}
	}
	return true
}

func finiteNumber(value json.Number) bool {
	parsed, err := strconv.ParseFloat(string(value), 64)
	return err == nil && !math.IsInf(parsed, 0) && !math.IsNaN(parsed)
}

func unitNumber(value json.Number) bool {
	parsed, err := strconv.ParseFloat(string(value), 64)
	return err == nil && !math.IsInf(parsed, 0) && !math.IsNaN(parsed) && parsed >= 0 && parsed <= 1
}

func validObject(value json.RawMessage) bool {
	if len(value) == 0 {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func hash(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func matchesFilter(filter map[string]struct{}, value string) bool {
	if len(filter) == 0 {
		return true
	}
	_, found := filter[value]
	return found
}
