package projector

import (
	"encoding/json"
	"fmt"
	"sort"
)

type FullPlan struct {
	Namespace, Version, ContentHash string
	Nodes                           []Node
	Edges                           []Edge
}
type DeltaPlan struct {
	Namespace, Version, BaseVersion, ContentHash string
	NodeUpserts                                  []Node
	NodeDeletes                                  []string
	EdgeUpserts                                  []Edge
	EdgeDeletes                                  []string
}
type Limits struct{ MaxPayloadBytes, MaxNodes, MaxEdges int }

func PlanFull(namespace, version string, target Result) (FullPlan, error) {
	return PlanFullWithLimits(namespace, version, target, Limits{})
}
func PlanFullWithLimits(namespace, version string, target Result, limits Limits) (FullPlan, error) {
	if err := validateResult(target); err != nil {
		return FullPlan{}, err
	}
	bytes, hash, err := ManifestBytes(target)
	_ = bytes
	if err != nil {
		return FullPlan{}, err
	}
	if namespace == "" || version == "" {
		return FullPlan{}, fmt.Errorf("snapshot identity is required")
	}
	if limits.MaxPayloadBytes > 0 && len(bytes) > limits.MaxPayloadBytes {
		return FullPlan{}, fmt.Errorf("manifest payload limit exceeded")
	}
	if limits.MaxNodes > 0 && len(target.Nodes) > limits.MaxNodes {
		return FullPlan{}, fmt.Errorf("node limit exceeded")
	}
	if limits.MaxEdges > 0 && len(target.Edges) > limits.MaxEdges {
		return FullPlan{}, fmt.Errorf("edge limit exceeded")
	}
	if _, err := ApplyDelta(Result{}, DeltaPlan{NodeUpserts: target.Nodes, EdgeUpserts: target.Edges}); err != nil {
		return FullPlan{}, err
	}
	return FullPlan{Namespace: namespace, Version: version, ContentHash: hash, Nodes: append([]Node(nil), target.Nodes...), Edges: append([]Edge(nil), target.Edges...)}, nil
}
func PlanDelta(namespace, version, baseVersion string, base, target Result) (DeltaPlan, error) {
	if namespace == "" || version == "" || baseVersion == "" {
		return DeltaPlan{}, fmt.Errorf("snapshot identity is required")
	}
	if err := validateResult(base); err != nil {
		return DeltaPlan{}, fmt.Errorf("invalid base manifest: %w", err)
	}
	if err := validateResult(target); err != nil {
		return DeltaPlan{}, fmt.Errorf("invalid target manifest: %w", err)
	}
	_, hash, err := ManifestBytes(target)
	if err != nil {
		return DeltaPlan{}, err
	}
	plan := DeltaPlan{Namespace: namespace, Version: version, BaseVersion: baseVersion, ContentHash: hash}
	bn := map[string]Node{}
	tn := map[string]Node{}
	be := map[string]Edge{}
	te := map[string]Edge{}
	for _, v := range base.Nodes {
		bn[v.ID] = v
	}
	for _, v := range target.Nodes {
		tn[v.ID] = v
	}
	for _, v := range base.Edges {
		be[v.ID] = v
	}
	for _, v := range target.Edges {
		te[v.ID] = v
	}
	for id, node := range tn {
		if old, ok := bn[id]; !ok || !same(old, node) {
			plan.NodeUpserts = append(plan.NodeUpserts, node)
		}
	}
	for id := range bn {
		if _, ok := tn[id]; !ok {
			plan.NodeDeletes = append(plan.NodeDeletes, id)
		}
	}
	for id, edge := range te {
		if old, ok := be[id]; !ok || !same(old, edge) {
			plan.EdgeUpserts = append(plan.EdgeUpserts, edge)
		}
	}
	for id := range be {
		if _, ok := te[id]; !ok {
			plan.EdgeDeletes = append(plan.EdgeDeletes, id)
		}
	}
	sort.Slice(plan.NodeUpserts, func(i, j int) bool { return plan.NodeUpserts[i].ID < plan.NodeUpserts[j].ID })
	sort.Strings(plan.NodeDeletes)
	sort.Slice(plan.EdgeUpserts, func(i, j int) bool { return plan.EdgeUpserts[i].ID < plan.EdgeUpserts[j].ID })
	sort.Strings(plan.EdgeDeletes)
	rebuilt, rebuildErr := ApplyDelta(base, plan)
	if rebuildErr != nil {
		return DeltaPlan{}, rebuildErr
	}
	_, rebuiltHash, rebuildErr := ManifestBytes(rebuilt)
	if rebuildErr != nil || rebuiltHash != hash {
		return DeltaPlan{}, fmt.Errorf("delta rematerialization mismatch")
	}
	return plan, nil
}
func ApplyDelta(base Result, plan DeltaPlan) (Result, error) {
	if err := validateResult(base); err != nil {
		return Result{}, fmt.Errorf("invalid base manifest: %w", err)
	}
	if err := validatePlan(plan); err != nil {
		return Result{}, err
	}
	nodes := map[string]Node{}
	edges := map[string]Edge{}
	for _, node := range base.Nodes {
		nodes[node.ID] = node
	}
	for _, edge := range base.Edges {
		edges[edge.ID] = edge
	}
	for _, id := range plan.NodeDeletes {
		delete(nodes, id)
	}
	for _, node := range plan.NodeUpserts {
		nodes[node.ID] = node
	}
	for _, id := range plan.EdgeDeletes {
		delete(edges, id)
	}
	for _, edge := range plan.EdgeUpserts {
		edges[edge.ID] = edge
	}
	result := Result{}
	for _, node := range nodes {
		result.Nodes = append(result.Nodes, node)
	}
	for _, edge := range edges {
		if _, from := nodes[edge.From]; !from {
			return Result{}, fmt.Errorf("dangling edge %s", edge.ID)
		}
		if _, to := nodes[edge.To]; !to {
			return Result{}, fmt.Errorf("dangling edge %s", edge.ID)
		}
		result.Edges = append(result.Edges, edge)
	}
	return result, nil
}

func validateResult(result Result) error {
	nodes := make(map[string]struct{}, len(result.Nodes))
	for _, node := range result.Nodes {
		if node.ID == "" {
			return fmt.Errorf("node id is required")
		}
		if _, exists := nodes[node.ID]; exists {
			return fmt.Errorf("duplicate node %s", node.ID)
		}
		nodes[node.ID] = struct{}{}
	}
	edges := make(map[string]struct{}, len(result.Edges))
	for _, edge := range result.Edges {
		if edge.ID == "" || edge.From == "" || edge.To == "" {
			return fmt.Errorf("edge identity is required")
		}
		if _, exists := edges[edge.ID]; exists {
			return fmt.Errorf("duplicate edge %s", edge.ID)
		}
		if _, exists := nodes[edge.From]; !exists {
			return fmt.Errorf("dangling edge %s", edge.ID)
		}
		if _, exists := nodes[edge.To]; !exists {
			return fmt.Errorf("dangling edge %s", edge.ID)
		}
		edges[edge.ID] = struct{}{}
	}
	return nil
}

func validatePlan(plan DeltaPlan) error {
	nodeUpserts := make(map[string]struct{}, len(plan.NodeUpserts))
	for _, node := range plan.NodeUpserts {
		if node.ID == "" {
			return fmt.Errorf("node upsert id is required")
		}
		if _, exists := nodeUpserts[node.ID]; exists {
			return fmt.Errorf("duplicate node upsert %s", node.ID)
		}
		nodeUpserts[node.ID] = struct{}{}
	}
	nodeDeletes := make(map[string]struct{}, len(plan.NodeDeletes))
	for _, id := range plan.NodeDeletes {
		if id == "" {
			return fmt.Errorf("node delete id is required")
		}
		if _, exists := nodeDeletes[id]; exists {
			return fmt.Errorf("duplicate node delete %s", id)
		}
		if _, exists := nodeUpserts[id]; exists {
			return fmt.Errorf("conflicting node operations for %s", id)
		}
		nodeDeletes[id] = struct{}{}
	}
	edgeUpserts := make(map[string]struct{}, len(plan.EdgeUpserts))
	for _, edge := range plan.EdgeUpserts {
		if edge.ID == "" {
			return fmt.Errorf("edge upsert id is required")
		}
		if _, exists := edgeUpserts[edge.ID]; exists {
			return fmt.Errorf("duplicate edge upsert %s", edge.ID)
		}
		edgeUpserts[edge.ID] = struct{}{}
	}
	edgeDeletes := make(map[string]struct{}, len(plan.EdgeDeletes))
	for _, id := range plan.EdgeDeletes {
		if id == "" {
			return fmt.Errorf("edge delete id is required")
		}
		if _, exists := edgeDeletes[id]; exists {
			return fmt.Errorf("duplicate edge delete %s", id)
		}
		if _, exists := edgeUpserts[id]; exists {
			return fmt.Errorf("conflicting edge operations for %s", id)
		}
		edgeDeletes[id] = struct{}{}
	}
	return nil
}
func same(a, b any) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}
