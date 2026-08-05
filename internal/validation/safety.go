package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

type TriggerEdge struct {
	From                string
	To                  string
	HasPositiveCount    bool
	HasPositiveDuration bool
	HasPositiveResource bool
}
type EventLoopFinding struct {
	Nodes        []string
	Edges        []TriggerEdge
	EvidenceHash string
}

// UnboundedEventLoops removes every edge with an explicit positive termination
// proof, then finds SCCs in the remaining graph. Any remaining cycle can run
// forever without interpreting user code.
func UnboundedEventLoops(edges []TriggerEdge) []EventLoopFinding {
	adj := map[string][]string{}
	nodes := map[string]struct{}{}
	unbounded := []TriggerEdge{}
	for _, edge := range edges {
		if edge.From == "" || edge.To == "" || edge.HasPositiveCount || edge.HasPositiveDuration || edge.HasPositiveResource {
			continue
		}
		unbounded = append(unbounded, edge)
		nodes[edge.From] = struct{}{}
		nodes[edge.To] = struct{}{}
		adj[edge.From] = append(adj[edge.From], edge.To)
	}
	for node := range adj {
		sort.Strings(adj[node])
	}
	components := stringSCC(nodes, adj)
	findings := []EventLoopFinding{}
	for _, component := range components {
		members := map[string]struct{}{}
		for _, node := range component {
			members[node] = struct{}{}
		}
		self := false
		if len(component) == 1 {
			for _, target := range adj[component[0]] {
				if target == component[0] {
					self = true
				}
			}
		}
		if len(component) < 2 && !self {
			continue
		}
		cycleEdges := []TriggerEdge{}
		for _, edge := range unbounded {
			if _, ok := members[edge.From]; ok {
				if _, ok := members[edge.To]; ok {
					cycleEdges = append(cycleEdges, edge)
				}
			}
		}
		sort.Slice(cycleEdges, func(i, j int) bool {
			return cycleEdges[i].From+"\x00"+cycleEdges[i].To < cycleEdges[j].From+"\x00"+cycleEdges[j].To
		})
		parts := append([]string{}, component...)
		for _, edge := range cycleEdges {
			parts = append(parts, edge.From+"->"+edge.To)
		}
		sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
		findings = append(findings, EventLoopFinding{component, cycleEdges, hex.EncodeToString(sum[:])})
	}
	return findings
}

type StackRuleInput struct {
	Operation      string
	Priority       *int
	MaxStacks      *int
	HasPositiveCap bool
}

func ValidateStackRule(rule StackRuleInput) []string {
	issues := []string{}
	switch rule.Operation {
	case "Add", "Multiply", "Override", "Max", "Min":
	default:
		return []string{"STACK_UNBOUNDED"}
	}
	if rule.Priority == nil {
		issues = append(issues, "STACK_PRIORITY_MISSING")
	}
	if rule.Operation == "Add" || rule.Operation == "Multiply" {
		if (rule.MaxStacks == nil || *rule.MaxStacks <= 0) && !rule.HasPositiveCap {
			issues = append(issues, "STACK_UNBOUNDED")
		}
	}
	if rule.MaxStacks != nil && *rule.MaxStacks <= 0 {
		issues = append(issues, "STACK_UNBOUNDED")
	}
	return issues
}

func stringSCC(nodes map[string]struct{}, adj map[string][]string) [][]string {
	keys := make([]string, 0, len(nodes))
	for node := range nodes {
		keys = append(keys, node)
	}
	sort.Strings(keys)
	index := 0
	indices := map[string]int{}
	low := map[string]int{}
	stack := []string{}
	onStack := map[string]bool{}
	components := [][]string{}
	var visit func(string)
	visit = func(node string) {
		index++
		indices[node] = index
		low[node] = index
		stack = append(stack, node)
		onStack[node] = true
		for _, next := range adj[node] {
			if indices[next] == 0 {
				visit(next)
				if low[next] < low[node] {
					low[node] = low[next]
				}
			} else if onStack[next] && indices[next] < low[node] {
				low[node] = indices[next]
			}
		}
		if low[node] != indices[node] {
			return
		}
		component := []string{}
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			component = append(component, last)
			if last == node {
				break
			}
		}
		sort.Strings(component)
		components = append(components, component)
	}
	for _, node := range keys {
		if indices[node] == 0 {
			visit(node)
		}
	}
	sort.Slice(components, func(i, j int) bool { return strings.Join(components[i], "\x00") < strings.Join(components[j], "\x00") })
	return components
}
