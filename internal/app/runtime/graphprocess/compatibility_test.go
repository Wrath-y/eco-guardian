package graphprocess

import (
	"net/http"
	"reflect"
	"testing"

	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

func compatibleHealth() graphsync.Health {
	return graphsync.Health{
		SchemaVersion: "1.0", Status: "ok", Service: "local-rag", ServiceVersion: "9.0.0", HTTPStatus: http.StatusOK,
		APIVersions: []string{"v1", "future"}, SupportedSchemaVersions: []string{"1.0", "future"},
		Capabilities: []graphsync.HealthState{{Name: "unknown", State: "future"}, {Name: "snapshot_lifecycle", State: "available"}, {Name: "task_polling", State: "available"}},
		Dependencies: []graphsync.HealthState{{Name: "unknown", State: "future"}, {Name: "sqlite", State: "available"}, {Name: "graph_migrations", State: "available"}, {Name: "core_graph_query", State: "available"}, {Name: "bm25", State: "available"}, {Name: "vector", State: "available"}, {Name: "rerank", State: "available"}},
		Limits:       []graphsync.Limit{{Name: "rebuild_components", Value: 3}, {Name: "future", Value: 1}},
	}
}

func TestCompatibilityAcceptsRequiredContractAndIgnoresUnknownAdditions(t *testing.T) {
	result := EvaluateHealthCompatibility(compatibleHealth(), nil)
	if !result.Compatible || len(result.Reasons) != 0 {
		t.Fatalf("result=%#v", result)
	}
	for _, operation := range result.Operations {
		if operation.State != OperationAvailable {
			t.Fatalf("operation=%#v", operation)
		}
	}
}

func TestCompatibilityEnforcesHTTPStatusAPIAndSchemaSemantics(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*graphsync.Health)
		reason string
	}{
		{"unavailable over 200", func(value *graphsync.Health) { value.Status = "unavailable" }, "HTTP_STATUS_MISMATCH"},
		{"ok over 503", func(value *graphsync.Health) { value.HTTPStatus = http.StatusServiceUnavailable }, "HTTP_STATUS_MISMATCH"},
		{"API missing", func(value *graphsync.Health) { value.APIVersions = []string{"v2"} }, "API_V1_UNSUPPORTED"},
		{"snapshot schema missing", func(value *graphsync.Health) { value.SupportedSchemaVersions = []string{"2.0"} }, "SNAPSHOT_SCHEMA_UNSUPPORTED"},
		{"health schema wrong", func(value *graphsync.Health) { value.SchemaVersion = "2.0" }, "HEALTH_SCHEMA_UNSUPPORTED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			health := compatibleHealth()
			test.mutate(&health)
			result := EvaluateHealthCompatibility(health, nil)
			if result.Compatible || !containsString(result.Reasons, test.reason) {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestCompatibilityReducesIndependentOperationInputs(t *testing.T) {
	health := compatibleHealth()
	for index := range health.Dependencies {
		if health.Dependencies[index].Name == "vector" {
			health.Dependencies[index].State = "degraded"
		}
		if health.Dependencies[index].Name == "rerank" {
			health.Dependencies[index].State = "unavailable"
		}
	}
	health.Status = "degraded"
	result := EvaluateHealthCompatibility(health, nil)
	if !result.Compatible {
		t.Fatalf("optional degradation rejected service: %#v", result)
	}
	if got := operationByID(result.Operations, OperationRetrievalVector); got.State != OperationDegraded || !reflect.DeepEqual(got.Reasons, []string{"DEPENDENCY_vector_DEGRADED"}) {
		t.Fatalf("vector=%#v", got)
	}
	if got := operationByID(result.Operations, OperationRetrievalRerank); got.State != OperationUnavailable || !reflect.DeepEqual(got.Reasons, []string{"DEPENDENCY_rerank_UNAVAILABLE"}) {
		t.Fatalf("rerank=%#v", got)
	}
	if got := operationByID(result.Operations, OperationCoreQuery); got.State != OperationAvailable {
		t.Fatalf("core=%#v", got)
	}
}

func TestCompatibilityRequiresLimitsAndRejectsDuplicateFacts(t *testing.T) {
	health := compatibleHealth()
	health.Limits[0].Value = 0
	result := EvaluateHealthCompatibility(health, nil)
	if got := operationByID(result.Operations, OperationExplicitRebuild); got.State != OperationUnavailable || !containsString(got.Reasons, "LIMIT_rebuild_components") {
		t.Fatalf("rebuild=%#v", got)
	}
	health = compatibleHealth()
	health.Capabilities = append(health.Capabilities, graphsync.HealthState{Name: "task_polling", State: "available"})
	result = EvaluateHealthCompatibility(health, nil)
	if result.Compatible || !containsString(result.Reasons, "CAPABILITY_DUPLICATE") {
		t.Fatalf("duplicate result=%#v", result)
	}
}

func TestCompatibilityMapsCoreAndOptionalRetrievalFailuresPrecisely(t *testing.T) {
	tests := []struct {
		dependency string
		core       OperationState
		fts        OperationState
		retrieval  OperationState
	}{
		{"sqlite", OperationUnavailable, OperationUnavailable, OperationUnavailable},
		{"graph_migrations", OperationUnavailable, OperationUnavailable, OperationUnavailable},
		{"core_graph_query", OperationUnavailable, OperationUnavailable, OperationUnavailable},
		{"bm25", OperationAvailable, OperationUnavailable, OperationDegraded},
		{"vector", OperationAvailable, OperationAvailable, OperationDegraded},
		{"rerank", OperationAvailable, OperationAvailable, OperationDegraded},
	}
	for _, test := range tests {
		t.Run(test.dependency, func(t *testing.T) {
			health := compatibleHealth()
			health.Status = "degraded"
			for index := range health.Dependencies {
				if health.Dependencies[index].Name == test.dependency {
					health.Dependencies[index].State = "unavailable"
				}
			}
			result := EvaluateHealthCompatibility(health, nil)
			if operationByID(result.Operations, OperationCoreQuery).State != test.core || operationByID(result.Operations, OperationGraphFTSReadiness).State != test.fts || result.Retrieval.State != test.retrieval {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestCompatibilityPreservesOneBaseRetrievalMode(t *testing.T) {
	health := compatibleHealth()
	health.Status = "degraded"
	for index := range health.Dependencies {
		if health.Dependencies[index].Name == "bm25" {
			health.Dependencies[index].State = "unavailable"
		}
	}
	result := EvaluateHealthCompatibility(health, nil)
	if result.Retrieval.State != OperationDegraded || operationByID(result.Operations, OperationRetrievalVector).State != OperationAvailable {
		t.Fatalf("one-mode retrieval=%#v", result.Retrieval)
	}
}
