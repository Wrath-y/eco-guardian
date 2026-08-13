package client

import (
	"sort"

	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

type Compatibility struct {
	Compatible        bool
	Warnings, Reasons []string
	Limits            map[string]int
}

// EvaluateCompatibility deliberately ignores service SemVer. The provider's
// advertised API/schema/capability/dependency contract is authoritative.
func EvaluateCompatibility(health graphsync.Health) Compatibility {
	result := Compatibility{Limits: make(map[string]int)}
	for _, limit := range health.Limits {
		if limit.Value >= 0 {
			result.Limits[limit.Name] = limit.Value
		}
	}
	if health.Status == "unavailable" {
		result.Reasons = append(result.Reasons, "HEALTH_UNAVAILABLE")
	}
	if !contains(health.APIVersions, "v1") {
		result.Reasons = append(result.Reasons, "API_V1_UNSUPPORTED")
	}
	if !contains(health.SupportedSchemaVersions, graphsync.SnapshotSchemaVersion) {
		result.Reasons = append(result.Reasons, "SNAPSHOT_SCHEMA_UNSUPPORTED")
	}
	for _, name := range []string{"snapshot_lifecycle", "task_polling"} {
		if state := stateFor(health.Capabilities, name); state != "available" {
			result.Reasons = append(result.Reasons, "CAPABILITY_"+name)
		}
	}
	// Snapshot lifecycle includes ready-only activation in the pinned v1 OpenAPI.
	for _, name := range []string{"sqlite", "graph_migrations", "core_graph_query", "bm25"} {
		if state := stateFor(health.Dependencies, name); state == "unavailable" || state == "disabled" || state == "" {
			result.Reasons = append(result.Reasons, "DEPENDENCY_"+name)
		}
	}
	for _, name := range []string{"vector", "rerank"} {
		if state := stateFor(health.Dependencies, name); state == "degraded" || state == "unavailable" || state == "disabled" {
			result.Warnings = append(result.Warnings, "DEGRADED_"+name)
		}
	}
	sort.Strings(result.Reasons)
	sort.Strings(result.Warnings)
	result.Compatible = len(result.Reasons) == 0
	return result
}
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func stateFor(values []graphsync.HealthState, name string) string {
	for _, value := range values {
		if value.Name == name {
			return value.State
		}
	}
	return ""
}
