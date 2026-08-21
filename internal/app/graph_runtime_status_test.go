package app

import (
	"context"
	"testing"
	"time"

	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

func TestGraphRuntimeStatusUsesCompatibilityContractAndSafeReasons(t *testing.T) {
	provider := &graphAppProviderFake{}
	providerHealth := graphsync.Health{Status: "degraded", APIVersions: []string{"v1"}, SupportedSchemaVersions: []string{"1.0"}, Capabilities: []graphsync.HealthState{{Name: "snapshot_lifecycle", State: "available"}, {Name: "task_polling", State: "available"}}, Dependencies: []graphsync.HealthState{{Name: "sqlite", State: "available"}, {Name: "graph_migrations", State: "available"}, {Name: "core_graph_query", State: "available"}, {Name: "bm25", State: "available"}, {Name: "vector", State: "degraded"}, {Name: "rerank", State: "available"}}}
	providerWithHealth := graphRuntimeProviderFake{graphAppProviderFake: provider, health: providerHealth}
	status := (GraphRuntimeStatusApplication{Provider: &providerWithHealth, Clock: func() time.Time { return time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC) }}).GraphRuntimeStatus(context.Background())
	if !status.Available || !status.Compatible || status.ObservedAt == nil || len(status.Degradations) != 1 || status.Degradations[0] != "DEGRADED_vector" || len(status.Reasons) != 0 {
		t.Fatalf("status=%#v", status)
	}

	unavailable := (GraphRuntimeStatusApplication{}).GraphRuntimeStatus(context.Background())
	if unavailable.Available || unavailable.Compatible || len(unavailable.Reasons) != 1 || unavailable.Reasons[0] != "GRAPH_PROVIDER_NOT_CONFIGURED" {
		t.Fatalf("unavailable=%#v", unavailable)
	}
}

type graphRuntimeProviderFake struct {
	*graphAppProviderFake
	health graphsync.Health
}

func (f *graphRuntimeProviderFake) Health(context.Context, string) (graphsync.Health, error) {
	return f.health, nil
}
