package app

import (
	"context"
	"time"

	"github.com/zouyi/eco-guardian/internal/graph/client"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

var graphRequiredCapabilities = []string{"snapshot_lifecycle", "task_polling", "activation", "core_graph_query", "bm25"}

// GraphRuntimeStatus is a safe, provider-payload-free health projection. Its
// reason strings are the same stable inputs used to disable Graph release
// capability; callers must not infer readiness from client-side health logic.
type GraphRuntimeStatus struct {
	Available, Compatible bool
	RequiredCapabilities  []string
	Degradations, Reasons []string
	ObservedAt            *time.Time
}

type GraphRuntimeStatusService interface {
	GraphRuntimeStatus(context.Context) GraphRuntimeStatus
}

type GraphRuntimeStatusApplication struct {
	Provider graphsync.GraphProvider
	Clock    func() time.Time
}

func (s GraphRuntimeStatusApplication) GraphRuntimeStatus(ctx context.Context) GraphRuntimeStatus {
	result := GraphRuntimeStatus{RequiredCapabilities: append([]string(nil), graphRequiredCapabilities...), Degradations: []string{}, Reasons: []string{}}
	if s.Provider == nil {
		result.Reasons = []string{"GRAPH_PROVIDER_NOT_CONFIGURED"}
		return result
	}
	now := time.Now().UTC()
	if s.Clock != nil {
		now = s.Clock().UTC()
	}
	result.ObservedAt = &now
	health, err := s.Provider.Health(ctx, "graph-runtime-health")
	if err != nil {
		result.Reasons = []string{"GRAPH_HEALTH_UNAVAILABLE"}
		return result
	}
	compatibility := client.EvaluateCompatibility(health)
	result.Available = health.Status != "unavailable"
	result.Compatible = compatibility.Compatible
	result.Degradations = append(result.Degradations, compatibility.Warnings...)
	result.Reasons = append(result.Reasons, compatibility.Reasons...)
	return result
}

var _ GraphRuntimeStatusService = GraphRuntimeStatusApplication{}
