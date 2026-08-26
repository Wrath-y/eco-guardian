package graphprocess

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fixedRequestIDs string

func (value fixedRequestIDs) NewRootRequestID() string { return string(value) }

func TestClientHealthAdapterReusesGraphClientLoopbackAndRequestIDContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/health" || request.Header.Get("X-Request-ID") != "root-health-1" {
			t.Errorf("request path=%s request-id=%s", request.URL.Path, request.Header.Get("X-Request-ID"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schema_version":"1.0","status":"ok","service":"local-rag","service_version":"9.0.0","api_versions":["v1"],"supported_schema_versions":["1.0"],"capabilities":[{"name":"snapshot_lifecycle","state":"available"},{"name":"task_polling","state":"available"}],"dependencies":[{"name":"sqlite","state":"available"},{"name":"graph_migrations","state":"available"},{"name":"core_graph_query","state":"available"},{"name":"bm25","state":"available"}],"limits":[]}`))
	}))
	defer server.Close()
	httpClient := server.Client()
	httpClient.Timeout = time.Second
	adapter := ClientHealthAdapter{HTTPClient: httpClient, RequestIDs: fixedRequestIDs("root-health-1")}
	observation, err := adapter.ProbeCompatibility(t.Context(), server.URL)
	if err != nil || !observation.Compatible || len(observation.Reasons) != 0 {
		t.Fatalf("observation=%#v err=%v", observation, err)
	}
	if _, err = adapter.ProbeCompatibility(t.Context(), "http://example.com:9400"); err == nil {
		t.Fatal("non-loopback endpoint accepted")
	}
}

func TestClientHealthAdapterMapsProviderFailureWithoutRawBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = writer.Write([]byte(`{"code":"GRAPH_STORE_UNAVAILABLE","message":"secret raw provider body","retryable":true,"details":{},"request_id":"provider-safe-1"}`))
	}))
	defer server.Close()
	httpClient := server.Client()
	httpClient.Timeout = time.Second
	adapter := ClientHealthAdapter{HTTPClient: httpClient, RequestIDs: fixedRequestIDs("root-health-2")}
	_, err := adapter.ProbeCompatibility(t.Context(), server.URL)
	var failure HealthProbeFailure
	if !errors.As(err, &failure) || failure.Code != "GRAPH_STORE_UNAVAILABLE" || failure.RequestID != "provider-safe-1" || !failure.Retryable || strings.Contains(err.Error(), "secret") {
		t.Fatalf("failure=%#v err=%v", failure, err)
	}
}

func TestClientHealthAdapterWaitReadyRecoversAfterReprobe(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte(`{"code":"STARTING","message":"starting","retryable":true,"details":{},"request_id":"provider-starting"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"schema_version":"1.0","status":"ok","service":"local-rag","service_version":"1.0.0","api_versions":["v1"],"supported_schema_versions":["1.0"],"capabilities":[{"name":"snapshot_lifecycle","state":"available"},{"name":"task_polling","state":"available"}],"dependencies":[{"name":"sqlite","state":"available"},{"name":"graph_migrations","state":"available"},{"name":"core_graph_query","state":"available"},{"name":"bm25","state":"available"}],"limits":[]}`))
	}))
	defer server.Close()
	httpClient := server.Client()
	httpClient.Timeout = time.Second
	adapter := ClientHealthAdapter{HTTPClient: httpClient, RequestIDs: fixedRequestIDs("root-health-3"), PollInterval: time.Millisecond}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := adapter.WaitReady(ctx, server.URL, 1); err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
