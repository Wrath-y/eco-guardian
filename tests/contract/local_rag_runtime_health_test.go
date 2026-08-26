package contract_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/app/runtime/graphprocess"
)

type runtimeHealthRequestIDs string

func (value runtimeHealthRequestIDs) NewRootRequestID() string { return string(value) }

func TestLocalRAGRuntimeConsumerReplaysHealthVariantsThroughActualClient(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("fixtures", "local-rag-graph-service-operability-v1", "health.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures map[string]map[string]any
	if err = json.Unmarshal(body, &fixtures); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		status     int
		compatible bool
		retrieval  graphprocess.OperationState
	}{
		{"ok", http.StatusOK, true, graphprocess.OperationAvailable},
		{"degraded", http.StatusOK, true, graphprocess.OperationDegraded},
		{"unavailable", http.StatusServiceUnavailable, false, graphprocess.OperationUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := cloneRuntimeHealthFixture(t, fixtures[test.name])
			fixture["unknown_additive_field"] = map[string]any{"future": true}
			fixture["capabilities"] = []any{
				map[string]any{"name": "snapshot_lifecycle", "state": "available"},
				map[string]any{"name": "task_polling", "state": "available"},
				map[string]any{"name": "future", "state": "future"},
			}
			vectorState := "available"
			if test.name == "degraded" {
				vectorState = "degraded"
			}
			fixture["dependencies"] = []any{
				map[string]any{"name": "sqlite", "state": "available"},
				map[string]any{"name": "graph_migrations", "state": "available"},
				map[string]any{"name": "core_graph_query", "state": "available"},
				map[string]any{"name": "bm25", "state": "available"},
				map[string]any{"name": "vector", "state": vectorState},
				map[string]any{"name": "rerank", "state": "available"},
			}
			fixture["limits"] = []any{map[string]any{"name": "rebuild_components", "value": float64(3)}}
			encoded, _ := json.Marshal(fixture)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("X-Request-ID") != "runtime-health-root" {
					t.Errorf("request ID=%q", request.Header.Get("X-Request-ID"))
				}
				writer.WriteHeader(test.status)
				_, _ = writer.Write(encoded)
			}))
			defer server.Close()
			client := server.Client()
			client.Timeout = time.Second
			adapter := graphprocess.ClientHealthAdapter{HTTPClient: client, RequestIDs: runtimeHealthRequestIDs("runtime-health-root")}
			compatibility, probeErr := adapter.ObserveHealth(t.Context(), server.URL)
			if probeErr != nil || compatibility.Compatible != test.compatible || compatibility.Retrieval.State != test.retrieval {
				t.Fatalf("compatibility=%#v err=%v", compatibility, probeErr)
			}
			if !sort.StringsAreSorted(compatibility.Reasons) || !sort.StringsAreSorted(compatibility.Retrieval.Reasons) {
				t.Fatalf("non-deterministic reasons=%#v", compatibility)
			}
		})
	}
}

func TestLocalRAGRuntimeConsumerMalformedTimeoutAndRecovery(t *testing.T) {
	responses := make(chan string, 3)
	responses <- `{"schema_version":"1.0","status":"future"}`
	responses <- `timeout`
	responses <- `{"schema_version":"1.0","status":"ok","service":"local-rag","service_version":"8.0.0","api_versions":["v1"],"supported_schema_versions":["1.0"],"capabilities":[{"name":"snapshot_lifecycle","state":"available"},{"name":"task_polling","state":"available"}],"dependencies":[{"name":"sqlite","state":"available"},{"name":"graph_migrations","state":"available"},{"name":"core_graph_query","state":"available"},{"name":"bm25","state":"available"},{"name":"vector","state":"available"},{"name":"rerank","state":"available"}],"limits":[{"name":"rebuild_components","value":3}]}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		response := <-responses
		if response == "timeout" {
			<-request.Context().Done()
			return
		}
		_, _ = writer.Write([]byte(response))
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	adapter := graphprocess.ClientHealthAdapter{HTTPClient: client, RequestIDs: runtimeHealthRequestIDs("runtime-health-recovery")}
	observer, err := graphprocess.NewHealthObserver(graphprocess.HealthObserverOptions{Source: adapter, Timeout: 20 * time.Millisecond, TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := observer.Observe(t.Context(), server.URL)
	observer.Invalidate()
	second, _ := observer.Observe(t.Context(), server.URL)
	observer.Invalidate()
	third, _ := observer.Observe(context.Background(), server.URL)
	if first.Reason != "HEALTH_CONTRACT_INVALID" || second.Reason != "HEALTH_TIMEOUT" || third.State != graphprocess.HealthHealthy || third.Generation != 3 {
		t.Fatalf("first=%#v second=%#v third=%#v", first, second, third)
	}
	if !contains(third.Compatibility.Diagnostics, "SERVICE_MAJOR_DIFFERENCE") {
		t.Fatalf("major diagnostic missing: %#v", third)
	}
}

func TestLocalRAGRuntimeConsumerSafeProviderIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte(`{"code":"GRAPH_STORE_UNAVAILABLE","message":"Bearer fixture-secret","retryable":true,"details":{},"request_id":"provider-safe-42"}`))
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = time.Second
	_, err := (graphprocess.ClientHealthAdapter{HTTPClient: client}).ObserveHealth(t.Context(), server.URL)
	var failure graphprocess.HealthProbeFailure
	if !errors.As(err, &failure) || failure.RequestID != "provider-safe-42" || failure.Code != "GRAPH_STORE_UNAVAILABLE" {
		t.Fatalf("failure=%#v err=%v", failure, err)
	}
}

func cloneRuntimeHealthFixture(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(value)
	var clone map[string]any
	if err := json.Unmarshal(body, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}
