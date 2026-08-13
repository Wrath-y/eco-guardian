package projector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// ManifestBytes emits the exact local-rag v1 final manifest. Provider v1
// fixes root member order; it hashes no job, mode, or base metadata.
func ManifestBytes(result Result) ([]byte, string, error) {
	nodes := append([]Node(nil), result.Nodes...)
	edges := append([]Edge(nil), result.Edges...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	type manifestNode struct {
		ID         string         `json:"id"`
		Type       string         `json:"type"`
		Label      string         `json:"label"`
		Text       string         `json:"text"`
		Properties map[string]any `json:"properties"`
		Provenance map[string]any `json:"provenance"`
	}
	type manifestEdge struct {
		ID           string         `json:"id"`
		From         string         `json:"from"`
		To           string         `json:"to"`
		Type         string         `json:"type"`
		RelationKind string         `json:"relation_kind"`
		Confidence   int            `json:"confidence"`
		Properties   map[string]any `json:"properties"`
		Provenance   map[string]any `json:"provenance"`
	}
	toNode := func(node Node) manifestNode {
		p := node.Provenance
		return manifestNode{node.ID, node.Type, node.Label, node.Text, node.Properties, map[string]any{"project_id": p.ProjectID, "revision_id": p.RevisionID, "config_hash": p.ConfigHash, "entity_id": p.EntityID, "entity_kind": p.EntityKind, "entity_schema_version": p.EntitySchemaVersion, "projection_schema_version": p.ProjectionSchema, "projector_version": p.Projector}}
	}
	toEdge := func(edge Edge) manifestEdge {
		p := edge.Provenance
		return manifestEdge{edge.ID, edge.From, edge.To, edge.Type, edge.RelationKind, edge.Confidence, edge.Properties, map[string]any{"project_id": p.ProjectID, "revision_id": p.RevisionID, "config_hash": p.ConfigHash, "source_entity_id": p.SourceEntityID, "field_path": p.FieldPath, "ordinal": p.Ordinal, "projection_schema_version": p.ProjectionSchema, "projector_version": p.Projector}}
	}
	encodedNodes := make([]manifestNode, len(nodes))
	for i, node := range nodes {
		encodedNodes[i] = toNode(node)
	}
	encodedEdges := make([]manifestEdge, len(edges))
	for i, edge := range edges {
		encodedEdges[i] = toEdge(edge)
	}
	canonical, err := json.Marshal(struct {
		SchemaVersion string         `json:"schema_version"`
		Nodes         []manifestNode `json:"nodes"`
		Edges         []manifestEdge `json:"edges"`
	}{"1.0", encodedNodes, encodedEdges})
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}
