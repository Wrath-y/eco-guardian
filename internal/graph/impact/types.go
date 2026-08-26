package impact

import (
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
)

const AnalysisContractVersion = "dependency-impact-v1"

type Direction string

const (
	DirectionIncoming Direction = "incoming"
	DirectionOutgoing Direction = "outgoing"
	DirectionBoth     Direction = "both"
)

func (d Direction) Valid() bool {
	return d == DirectionIncoming || d == DirectionOutgoing || d == DirectionBoth
}

type ResultMode string

const (
	ReverseDependencyImpact ResultMode = "reverse_dependency_impact"
	RelationshipExploration ResultMode = "relationship_exploration"
)

type TruncationReason string

const (
	MaxDepth TruncationReason = "MAX_DEPTH"
	MaxNodes TruncationReason = "MAX_NODES"
	MaxPaths TruncationReason = "MAX_PATHS"
)

type SuspectedState string

const (
	SuspectedDisabled        SuspectedState = "disabled"
	SuspectedReady           SuspectedState = "ready"
	SuspectedDegraded        SuspectedState = "degraded"
	SuspectedRebuildRequired SuspectedState = "rebuild_required"
	SuspectedUnavailable     SuspectedState = "unavailable"
	SuspectedCanceled        SuspectedState = "canceled"
)

type RevisionIdentity struct {
	RevisionID          domain.ID `json:"revision_id"`
	ConfigHash          string    `json:"config_hash"`
	VersionManifestHash string    `json:"version_manifest_hash"`
	GraphManifestHash   string    `json:"graph_manifest_hash"`
	GraphNodeCount      int       `json:"graph_node_count"`
	GraphEdgeCount      int       `json:"graph_edge_count"`
}

func (i RevisionIdentity) Valid() bool {
	return i.RevisionID.Valid() && ValidHash(i.ConfigHash) && ValidHash(i.VersionManifestHash) && ValidHash(i.GraphManifestHash) && i.GraphNodeCount >= 0 && i.GraphEdgeCount >= 0
}

type Filters struct {
	RelationshipKinds []string  `json:"relationship_kinds"`
	NodeTypes         []string  `json:"node_types"`
	EdgeTypes         []string  `json:"edge_types"`
	Direction         Direction `json:"direction"`
}

type Limits struct {
	MaxDepth              int `json:"max_depth"`
	MaxNodes              int `json:"max_nodes"`
	DefaultPathsPerTarget int `json:"default_paths_per_target"`
	ExpandedMaxPaths      int `json:"expanded_max_paths"`
}

type SuspectedOptions struct {
	Enabled       bool `json:"enabled"`
	MaxSeeds      int  `json:"max_seeds"`
	MaxResults    int  `json:"max_results"`
	GraphMaxDepth int  `json:"graph_max_depth"`
}

type Command struct {
	ProjectID        domain.ID        `json:"project_uuid"`
	BaseRevisionID   domain.ID        `json:"base_revision_id"`
	TargetRevisionID domain.ID        `json:"target_revision_id"`
	Filters          Filters          `json:"filters"`
	Limits           Limits           `json:"limits"`
	Suspected        SuspectedOptions `json:"suspected"`
}

type Input struct {
	ProjectID               domain.ID        `json:"project_uuid"`
	Base                    RevisionIdentity `json:"base"`
	Target                  RevisionIdentity `json:"target"`
	AnalysisContractVersion string           `json:"analysis_contract_version"`
	Filters                 Filters          `json:"filters"`
	Limits                  Limits           `json:"limits"`
	Suspected               SuspectedOptions `json:"suspected"`
}

type ChangedEntity struct {
	EntityID      domain.ID                 `json:"entity_id"`
	Kind          domain.EntityKind         `json:"kind"`
	ChangeKind    versioningdiff.ChangeKind `json:"change_kind"`
	FieldPaths    []string                  `json:"field_paths"`
	TargetNodeID  string                    `json:"target_node_id,omitempty"`
	QueryEligible bool                      `json:"query_eligible"`
	Ineligibility string                    `json:"ineligibility,omitempty"`
}

type NodeDepth struct {
	Node  graphsync.Node `json:"node"`
	Depth int            `json:"depth"`
}

type TraverseRequest struct {
	Namespace         string    `json:"namespace"`
	SnapshotVersion   string    `json:"snapshot_version"`
	StartNodeIDs      []string  `json:"start_node_ids"`
	RelationshipKinds []string  `json:"relationship_kinds"`
	NodeTypes         []string  `json:"node_types,omitempty"`
	EdgeTypes         []string  `json:"edge_types,omitempty"`
	Direction         Direction `json:"direction"`
	MaxDepth          int       `json:"max_depth"`
	MaxNodes          int       `json:"max_nodes"`
}

type TraverseResponse struct {
	ResolvedSnapshotVersion string             `json:"resolved_snapshot_version"`
	ContentHash             string             `json:"content_hash"`
	Nodes                   []NodeDepth        `json:"nodes"`
	Edges                   []graphsync.Edge   `json:"edges"`
	Truncated               bool               `json:"truncated"`
	TruncationReasons       []TruncationReason `json:"truncation_reasons"`
	Warnings                []string           `json:"warnings"`
}

type Path struct {
	SourceNodeID string             `json:"source_node_id"`
	TargetNodeID string             `json:"target_node_id"`
	NodeIDs      []string           `json:"node_ids"`
	EdgeIDs      []string           `json:"edge_ids"`
	Nodes        []graphsync.Node   `json:"nodes"`
	Edges        []graphsync.Edge   `json:"edges"`
	HopCount     int                `json:"hop_count"`
	Truncated    bool               `json:"truncated"`
	Reasons      []TruncationReason `json:"truncation_reasons"`
}

type PathsRequest struct {
	Namespace         string    `json:"namespace"`
	SnapshotVersion   string    `json:"snapshot_version"`
	SourceNodeIDs     []string  `json:"source_node_ids"`
	TargetNodeIDs     []string  `json:"target_node_ids"`
	RelationshipKinds []string  `json:"relationship_kinds"`
	NodeTypes         []string  `json:"node_types,omitempty"`
	EdgeTypes         []string  `json:"edge_types,omitempty"`
	Direction         Direction `json:"direction"`
	MaxDepth          int       `json:"max_depth"`
	MaxNodes          int       `json:"max_nodes"`
	MaxPaths          int       `json:"max_paths"`
}

type PathsResponse struct {
	ResolvedSnapshotVersion string             `json:"resolved_snapshot_version"`
	ContentHash             string             `json:"content_hash"`
	Paths                   []Path             `json:"paths"`
	Truncated               bool               `json:"truncated"`
	TruncationReasons       []TruncationReason `json:"truncation_reasons"`
	Warnings                []string           `json:"warnings"`
}

type RetrievalScores struct {
	BM25Rank    *int    `json:"bm25_rank,omitempty"`
	VectorRank  *int    `json:"vector_rank,omitempty"`
	BM25Score   *string `json:"bm25_score,omitempty"`
	VectorScore *string `json:"vector_score,omitempty"`
	RRFScore    *string `json:"rrf_score,omitempty"`
	GraphScore  *string `json:"graph_score,omitempty"`
	RerankScore *string `json:"rerank_score,omitempty"`
}

type SuspectedEvidence struct {
	Rank              int             `json:"rank"`
	Node              graphsync.Node  `json:"node"`
	CitationText      string          `json:"citation_text"`
	SeedNodeID        string          `json:"seed_node_id"`
	Path              *Path           `json:"path,omitempty"`
	RelationshipKinds []string        `json:"relationship_kinds"`
	Scores            RetrievalScores `json:"scores"`
	FTSGeneration     string          `json:"fts_generation,omitempty"`
	VectorGeneration  string          `json:"vector_generation,omitempty"`
	AlgorithmVersion  string          `json:"algorithm_version"`
	ModelProvider     string          `json:"model_provider,omitempty"`
	Model             string          `json:"model,omitempty"`
}

type RetrieveRequest struct {
	Namespace         string   `json:"namespace"`
	SnapshotVersion   string   `json:"snapshot_version"`
	Query             string   `json:"query"`
	RelationshipKinds []string `json:"relationship_kinds"`
	NodeTypes         []string `json:"node_types,omitempty"`
	EdgeTypes         []string `json:"edge_types,omitempty"`
	MaxSeeds          int      `json:"max_seeds"`
	MaxResults        int      `json:"max_results"`
	GraphMaxDepth     int      `json:"graph_max_depth"`
}

type RetrieveResponse struct {
	ResolvedSnapshotVersion string              `json:"resolved_snapshot_version"`
	ContentHash             string              `json:"content_hash"`
	Mode                    string              `json:"mode"`
	Degraded                bool                `json:"degraded"`
	Warnings                []string            `json:"warnings"`
	Results                 []SuspectedEvidence `json:"results"`
}

type AffectedEntity struct {
	Node         graphsync.Node `json:"node"`
	MinimumDepth int            `json:"minimum_depth"`
	Direct       bool           `json:"direct"`
	Indirect     bool           `json:"indirect"`
	TagRule      bool           `json:"tag_rule"`
	DefaultPath  *Path          `json:"default_path,omitempty"`
}

type Report struct {
	ID             domain.ID           `json:"id"`
	Input          Input               `json:"input"`
	InputHash      string              `json:"input_hash"`
	ResultHash     string              `json:"result_hash"`
	Mode           ResultMode          `json:"mode"`
	Changed        []ChangedEntity     `json:"changed_entities"`
	Affected       []AffectedEntity    `json:"deterministic_affected"`
	Suspected      []SuspectedEvidence `json:"suspected_associations"`
	SuspectedState SuspectedState      `json:"suspected_state"`
	Truncated      bool                `json:"truncated"`
	Reasons        []TruncationReason  `json:"truncation_reasons"`
	Warnings       []string            `json:"warnings"`
	CreatedAt      time.Time           `json:"created_at"`
}

func ValidHash(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}
