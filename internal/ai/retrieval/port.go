package retrieval

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf8"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

const (
	MaxQueryBytes  = 8_192
	MaxFilterItems = 100
)

type Filters struct {
	NodeTypes []string `json:"node_types,omitempty"`
	EdgeTypes []string `json:"edge_types,omitempty"`
}

func (v Filters) Valid() bool {
	return validFilter(v.NodeTypes) && validFilter(v.EdgeTypes)
}

type Request struct {
	Base    aicontract.FrozenBaseIdentity
	Query   string
	Filters Filters
	Budget  aicontract.Budget
}

func (v Request) Valid() bool {
	return v.Base.Valid() && v.Budget.Valid() && v.Query == strings.TrimSpace(v.Query) && v.Query != "" && utf8.ValidString(v.Query) && len(v.Query) <= MaxQueryBytes && v.Filters.Valid()
}

type Generation struct {
	Component     string `json:"component"`
	Generation    string `json:"generation"`
	Algorithm     string `json:"algorithm"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	Dimensions    int    `json:"dimensions,omitempty"`
	Tokenizer     string `json:"tokenizer,omitempty"`
	ContentDigest string `json:"content_digest"`
}

type Warning struct {
	Stage     string `json:"stage"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type Node struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Label      string          `json:"label"`
	Text       string          `json:"text"`
	Properties json.RawMessage `json:"properties"`
	Provenance json.RawMessage `json:"provenance"`
}

type Edge struct {
	ID           string          `json:"id"`
	From         string          `json:"from"`
	To           string          `json:"to"`
	Type         string          `json:"type"`
	RelationKind string          `json:"relation_kind"`
	Confidence   json.Number     `json:"confidence"`
	Properties   json.RawMessage `json:"properties"`
	Provenance   json.RawMessage `json:"provenance"`
}

type Scores struct {
	BM25Rank    *int         `json:"bm25_rank,omitempty"`
	BM25Score   *json.Number `json:"bm25_score,omitempty"`
	VectorRank  *int         `json:"vector_rank,omitempty"`
	VectorScore *json.Number `json:"vector_score,omitempty"`
	RRFScore    json.Number  `json:"rrf_score"`
	GraphScore  json.Number  `json:"graph_score"`
	RerankScore *json.Number `json:"rerank_score,omitempty"`
}

type SeedEvidence struct {
	NodeID     string `json:"node_id"`
	SearchText string `json:"search_text"`
}

type PathEvidence struct {
	NodeIDs       []string `json:"node_ids"`
	EdgeIDs       []string `json:"edge_ids"`
	Nodes         []Node   `json:"nodes"`
	Edges         []Edge   `json:"edges"`
	ExplicitEdges []Edge   `json:"explicit_edges"`
	InferredEdges []Edge   `json:"inferred_edges"`
}

type Evidence struct {
	Seed *SeedEvidence `json:"seed,omitempty"`
	Path *PathEvidence `json:"path,omitempty"`
}

type Result struct {
	Rank           int         `json:"rank"`
	Node           Node        `json:"node"`
	CitationText   string      `json:"citation_text"`
	HopCount       int         `json:"hop_count"`
	PathConfidence json.Number `json:"path_confidence"`
	Scores         Scores      `json:"scores"`
	Evidence       Evidence    `json:"evidence"`
}

type Response struct {
	Request                 Request     `json:"-"`
	ResolvedSnapshotVersion string      `json:"resolved_snapshot_version"`
	ContentHash             string      `json:"content_hash"`
	ModeUsed                string      `json:"mode_used"`
	Degraded                bool        `json:"degraded"`
	Warnings                []Warning   `json:"warnings"`
	FTSGeneration           *Generation `json:"fts_generation,omitempty"`
	VectorGeneration        *Generation `json:"vector_generation,omitempty"`
	Rerank                  string      `json:"rerank"`
	Results                 []Result    `json:"results"`
}

type Port interface {
	Retrieve(context.Context, Request) (Response, error)
}

// BuildV1Request derives the provider query and node filter only from the
// already frozen canonical input. Provider/model output is never accepted as
// a retrieval query or filter source.
func BuildV1Request(input aicontract.AIDesignInputV1) (Request, error) {
	if !input.Valid() {
		return Request{}, aicontract.ErrRegistryInvalid
	}
	parts := make([]string, 0, len(input.Goals)+len(input.Metrics)+len(input.Constraints)+len(input.Scenes))
	for _, goal := range input.Goals {
		parts = append(parts, "goal "+goal.ID+": "+goal.Description)
	}
	for _, metric := range input.Metrics {
		parts = append(parts, "metric "+metric.MetricID+" "+string(metric.Direction)+" "+metric.Target+" "+metric.Unit)
	}
	for _, constraint := range input.Constraints {
		parts = append(parts, "constraint "+constraint.ID+" "+string(constraint.Path)+" "+string(constraint.Operator)+" "+string(constraint.Value))
	}
	for _, scene := range input.Scenes {
		parts = append(parts, "scene "+scene)
	}
	sort.Strings(parts)
	nodeTypes := make([]string, 0, len(input.AllowedTargets))
	seenKinds := map[string]struct{}{}
	for _, target := range input.AllowedTargets {
		if _, found := seenKinds[target.Kind]; !found {
			nodeTypes = append(nodeTypes, target.Kind)
			seenKinds[target.Kind] = struct{}{}
		}
	}
	sort.Strings(nodeTypes)
	request := Request{Base: input.Base, Query: strings.Join(parts, "; "), Filters: Filters{NodeTypes: nodeTypes}, Budget: input.Budget}
	if !request.Valid() {
		return Request{}, aicontract.ErrRegistryInvalid
	}
	return request, nil
}

func validFilter(values []string) bool {
	if len(values) > MaxFilterItems {
		return false
	}
	seen := map[string]struct{}{}
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) || len(value) > 128 {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
