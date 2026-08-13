package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

func TestClientRejectsNonLoopback(t *testing.T) {
	if _, err := New(Config{Endpoint: "https://provider.example"}); !errors.Is(err, ErrEndpointNotLoopback) {
		t.Fatalf("err=%v", err)
	}
}

func TestPutUsesEncodedPathRequestIDAndNoIdempotencyKey(t *testing.T) {
	seen := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = true
		if r.URL.EscapedPath() != "/v1/graphs/project%2Fone/snapshots/revision%2Fone" {
			t.Errorf("path=%q", r.URL.EscapedPath())
		}
		if r.Header.Get("X-Request-ID") != "root-request" {
			t.Errorf("request ID not propagated")
		}
		if r.Header.Get("Idempotency-Key") != "" {
			t.Errorf("Snapshot PUT must not send Idempotency-Key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"namespace":"project/one","version":"revision/one","base_version":null,"schema_version":"1.0","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","node_count":0,"edge_count":0,"task_id":"task-1","status":"building","query_ready":false,"components":[],"warnings":[]}`))
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.PutSnapshot(context.Background(), "project/one", "revision/one", graphsync.PutSnapshotRequest{SchemaVersion: "1.0", Mode: "full", ContentHash: strings.Repeat("a", 64), Nodes: []graphsync.Node{}, Edges: []graphsync.Edge{}}, "root-request")
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("server was not called")
	}
}

func TestHealthAllowsUnknownFieldsAndCompatibilityData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"schema_version":"1.0","status":"degraded","service":"local-rag","service_version":"1","api_versions":["v1"],"supported_schema_versions":["1.0"],"capabilities":[{"name":"vector","state":"degraded"}],"limits":[{"name":"nodes","value":1}],"dependencies":[],"future_field":true}`))
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	health, err := client.Health(context.Background(), "request-1")
	if err != nil || health.Status != "degraded" || len(health.Capabilities) != 1 {
		t.Fatalf("health=%#v err=%v", health, err)
	}
}

func TestRetryPolicyHonorsMaxRetriesAndPermanentProviderError(t *testing.T) {
	count := 0
	policy := RetryPolicy{BaseDelay: time.Nanosecond, Sleep: func(context.Context, time.Duration) error { return nil }}
	err := policy.Do(context.Background(), func(context.Context, int) error { count++; return context.DeadlineExceeded })
	if !errors.Is(err, context.DeadlineExceeded) || count != 3 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	count = 0
	err = policy.Do(context.Background(), func(context.Context, int) error {
		count++
		return &graphsync.ProviderError{Code: "CONTENT_HASH_CONFLICT", Message: "conflict", RequestID: "r", Details: map[string]any{}, Retryable: false}
	})
	if count != 1 || err == nil {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestRetryCorrelationAndSafeDiagnosticsExcludeRawProviderData(t *testing.T) {
	policy := RetryPolicy{BaseDelay: time.Nanosecond, Sleep: func(context.Context, time.Duration) error { return nil }}
	var attempts []RetryAttempt
	err := policy.DoWithCorrelation(context.Background(), "root-1", func(_ context.Context, attempt RetryAttempt) error {
		attempts = append(attempts, attempt)
		if attempt.Number < 2 {
			return context.DeadlineExceeded
		}
		return nil
	})
	if err != nil || len(attempts) != 2 || attempts[0].AttemptRequestID != "root-1.1" || attempts[1].AttemptRequestID != "root-1.2" {
		t.Fatalf("attempts=%#v err=%v", attempts, err)
	}
	diagnostic := NewSafeDiagnostic("root-1", "root-1.1", &graphsync.ProviderError{Code: "TASK_NOT_FOUND", Message: "graph text /filesystem/path", RequestID: "provider-1", Details: map[string]any{"body": "raw Graph"}})
	if strings.Contains(diagnostic.ProviderCode+diagnostic.ProviderRequestID, "Graph") || diagnostic.ProviderCode != "TASK_NOT_FOUND" {
		t.Fatalf("diagnostic=%#v", diagnostic)
	}
}

func TestCompatibilityUsesContractAndPreservesOptionalVectorWarning(t *testing.T) {
	health := graphsync.Health{Status: "degraded", APIVersions: []string{"v1"}, SupportedSchemaVersions: []string{"1.0"}, Capabilities: []graphsync.HealthState{{Name: "snapshot_lifecycle", State: "available"}, {Name: "task_polling", State: "available"}}, Dependencies: []graphsync.HealthState{{Name: "sqlite", State: "available"}, {Name: "graph_migrations", State: "available"}, {Name: "core_graph_query", State: "available"}, {Name: "bm25", State: "available"}, {Name: "vector", State: "degraded"}}}
	compatibility := EvaluateCompatibility(health)
	if !compatibility.Compatible || len(compatibility.Warnings) != 1 || compatibility.Warnings[0] != "DEGRADED_vector" {
		t.Fatalf("%#v", compatibility)
	}
	health.Capabilities[0].State = "unavailable"
	if EvaluateCompatibility(health).Compatible {
		t.Fatal("missing required lifecycle capability must reject sync")
	}
}

func TestErrorCatalogIsCodeOnlyAndSafe(t *testing.T) {
	error := &graphsync.ProviderError{Code: "CONTENT_HASH_CONFLICT", Message: "raw graph text must not escape", RequestID: "request-2", Details: map[string]any{"raw": "secret"}}
	safe := SafeProviderError(error)
	if safe.Kind != ErrorHashConflict || safe.Code != error.Code || safe.RequestID != error.RequestID || strings.Contains(safe.Code, "raw") {
		t.Fatalf("%#v", safe)
	}
}

func TestClientHandlesReplayAndProviderErrorBranches(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 3 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"BASE_SNAPSHOT_NOT_FOUND","message":"base missing","retryable":false,"details":{},"request_id":"provider-base"}`))
			return
		}
		if calls == 4 {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"CONTENT_HASH_CONFLICT","message":"conflict","retryable":false,"details":{},"request_id":"provider-conflict"}`))
			return
		}
		if calls == 1 {
			w.WriteHeader(http.StatusAccepted)
		}
		_, _ = w.Write([]byte(`{"namespace":"project","version":"revision","base_version":null,"schema_version":"1.0","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","node_count":0,"edge_count":0,"task_id":"task-1","status":"building","query_ready":false,"components":[],"warnings":[]}`))
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	request := graphsync.PutSnapshotRequest{SchemaVersion: "1.0", Mode: "full", ContentHash: strings.Repeat("a", 64), Nodes: []graphsync.Node{}, Edges: []graphsync.Edge{}}
	if _, err = client.PutSnapshot(context.Background(), "project", "revision", request, "request-1"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.PutSnapshot(context.Background(), "project", "revision", request, "request-2"); err != nil {
		t.Fatal(err)
	}
	_, err = client.PutSnapshot(context.Background(), "project", "revision", request, "request-3")
	if provider, ok := err.(*graphsync.ProviderError); !ok || ClassifyError(provider) != ErrorBaseUnavailable || provider.RequestID != "provider-base" {
		t.Fatalf("err=%#v", err)
	}
	_, err = client.PutSnapshot(context.Background(), "project", "revision", request, "request-4")
	if provider, ok := err.(*graphsync.ProviderError); !ok || ClassifyError(provider) != ErrorHashConflict {
		t.Fatalf("err=%#v", err)
	}
}

func TestTaskSnapshotAndActivationContractValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tasks/task-1":
			_, _ = w.Write([]byte(`{"task_id":"task-1","operation":"snapshot_build","namespace":"project","snapshot_version":"revision","state":"succeeded","phase":"completed","progress":1,"warnings":[],"created_at":"2026-08-01T00:00:00Z"}`))
		case "/v1/graphs/project/snapshots/revision":
			_, _ = w.Write([]byte(`{"namespace":"project","version":"revision","base_version":null,"schema_version":"1.0","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","node_count":2,"edge_count":1,"task_id":"task-1","status":"ready","query_ready":true,"components":[{"name":"graph","state":"ready"},{"name":"fts","state":"ready"},{"name":"vector","state":"unavailable"}],"warnings":["VECTOR_UNAVAILABLE"]}`))
		default:
			_, _ = w.Write([]byte(`{"namespace":"project","active_version":"revision","changed":false}`))
		}
	}))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.GetTask(context.Background(), "task-1", "request-1")
	if err != nil || task.State != "succeeded" {
		t.Fatalf("task=%#v err=%v", task, err)
	}
	snapshot, err := client.InspectSnapshot(context.Background(), "project", "revision", "request-2")
	if err != nil || !snapshot.QueryReady || snapshot.Components[2].State != "unavailable" {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	activation, err := client.ActivateSnapshot(context.Background(), "project", "revision", "request-3")
	if err != nil || activation.Changed {
		t.Fatalf("activation=%#v err=%v", activation, err)
	}
}

func TestClientRejectsOversizedAndInvalidRequiredResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", 32))) }))
	defer server.Close()
	client, err := New(Config{Endpoint: server.URL, MaxResponseBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Health(context.Background(), "request-1"); !errors.Is(err, ErrContract) {
		t.Fatalf("err=%v", err)
	}
}
