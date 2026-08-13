package projector

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
)

// ManifestBytes emits the exact local-rag v1 final manifest. JCS is pinned in
// go.mod and hashes only schema_version/nodes/edges, never job or mode data.
func ManifestBytes(result Result) ([]byte, string, error) {
	nodes := append([]Node(nil), result.Nodes...)
	edges := append([]Edge(nil), result.Edges...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	toNode := func(node Node) map[string]any {
		p := node.Provenance
		return map[string]any{"id": node.ID, "type": node.Type, "label": node.Label, "text": node.Text, "properties": node.Properties, "provenance": map[string]any{"project_id": p.ProjectID, "revision_id": p.RevisionID, "config_hash": p.ConfigHash, "entity_id": p.EntityID, "entity_kind": p.EntityKind, "entity_schema_version": p.EntitySchemaVersion, "projection_schema_version": p.ProjectionSchema, "projector_version": p.Projector}}
	}
	toEdge := func(edge Edge) map[string]any {
		p := edge.Provenance
		return map[string]any{"id": edge.ID, "from": edge.From, "to": edge.To, "type": edge.Type, "relation_kind": edge.RelationKind, "confidence": edge.Confidence, "properties": edge.Properties, "provenance": map[string]any{"project_id": p.ProjectID, "revision_id": p.RevisionID, "config_hash": p.ConfigHash, "source_entity_id": p.SourceEntityID, "field_path": p.FieldPath, "ordinal": p.Ordinal, "projection_schema_version": p.ProjectionSchema, "projector_version": p.Projector}}
	}
	encodedNodes := make([]map[string]any, len(nodes))
	for i, node := range nodes {
		encodedNodes[i] = toNode(node)
	}
	encodedEdges := make([]map[string]any, len(edges))
	for i, edge := range edges {
		encodedEdges[i] = toEdge(edge)
	}
	raw, err := json.Marshal(map[string]any{"schema_version": "1.0", "nodes": encodedNodes, "edges": encodedEdges})
	if err != nil {
		return nil, "", err
	}
	canonical, err := jsoncanonicalizer.Transform(raw)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}
