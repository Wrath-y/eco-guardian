package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type FormulaNode struct {
	EntityID          domain.ID
	FieldPath         string
	OutputAttributeID domain.ID
}

func (n FormulaNode) key() string {
	return string(n.EntityID) + "|" + n.FieldPath + "|" + string(n.OutputAttributeID)
}

type FormulaEdge struct {
	From FormulaNode
	To   FormulaNode
}
type CycleFinding struct {
	Node      FormulaNode
	Nodes     []FormulaNode
	Edges     []FormulaEdge
	CycleHash string
}

// StaticFormulaCycles uses Tarjan SCC over a canonically ordered graph. A
// self-edge is a cycle; every member of a multi-node SCC receives identical
// evidence and hash.
func StaticFormulaCycles(nodes []FormulaNode, edges []FormulaEdge) []CycleFinding {
	byKey := map[string]FormulaNode{}
	for _, node := range nodes {
		byKey[node.key()] = node
	}
	for _, edge := range edges {
		byKey[edge.From.key()] = edge.From
		byKey[edge.To.key()] = edge.To
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	adj := map[string][]string{}
	for _, edge := range edges {
		adj[edge.From.key()] = append(adj[edge.From.key()], edge.To.key())
	}
	for key := range adj {
		sort.Strings(adj[key])
	}
	index := 0
	indices := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	stack := []string{}
	findings := []CycleFinding{}
	var visit func(string)
	visit = func(key string) {
		index++
		indices[key] = index
		low[key] = index
		stack = append(stack, key)
		onStack[key] = true
		for _, next := range adj[key] {
			if indices[next] == 0 {
				visit(next)
				if low[next] < low[key] {
					low[key] = low[next]
				}
			} else if onStack[next] && indices[next] < low[key] {
				low[key] = indices[next]
			}
		}
		if low[key] != indices[key] {
			return
		}
		component := []string{}
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			component = append(component, last)
			if last == key {
				break
			}
		}
		sort.Strings(component)
		self := false
		if len(component) == 1 {
			for _, next := range adj[component[0]] {
				if next == component[0] {
					self = true
				}
			}
		}
		if len(component) < 2 && !self {
			return
		}
		componentNodes := make([]FormulaNode, len(component))
		member := map[string]struct{}{}
		for i, value := range component {
			componentNodes[i] = byKey[value]
			member[value] = struct{}{}
		}
		componentEdges := []FormulaEdge{}
		for _, edge := range edges {
			if _, ok := member[edge.From.key()]; ok {
				if _, ok := member[edge.To.key()]; ok {
					componentEdges = append(componentEdges, edge)
				}
			}
		}
		sort.Slice(componentEdges, func(i, j int) bool {
			return componentEdges[i].From.key()+"->"+componentEdges[i].To.key() < componentEdges[j].From.key()+"->"+componentEdges[j].To.key()
		})
		hash := cycleHash(component, componentEdges)
		for _, node := range componentNodes {
			findings = append(findings, CycleFinding{node, componentNodes, componentEdges, hash})
		}
	}
	for _, key := range keys {
		if indices[key] == 0 {
			visit(key)
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Node.key() < findings[j].Node.key() })
	return findings
}
func cycleHash(nodes []string, edges []FormulaEdge) string {
	parts := append([]string{}, nodes...)
	for _, edge := range edges {
		parts = append(parts, edge.From.key()+"->"+edge.To.key())
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}
