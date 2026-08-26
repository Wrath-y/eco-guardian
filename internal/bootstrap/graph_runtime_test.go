package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	"github.com/zouyi/eco-guardian/internal/app/runtime/graphprocess"
	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

const compatibleHealth = `{"schema_version":"1.0","status":"ok","service":"local-rag","service_version":"9.0.0","api_versions":["v1"],"supported_schema_versions":["1.0"],"capabilities":[{"name":"snapshot_lifecycle","state":"available"},{"name":"task_polling","state":"available"}],"dependencies":[{"name":"sqlite","state":"available"},{"name":"graph_migrations","state":"available"},{"name":"core_graph_query","state":"available"},{"name":"bm25","state":"available"}],"limits":[]}`

func TestGraphRuntimeDependencySelectsExternalAndReprobesWithoutOwningIt(t *testing.T) {
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if unavailable.Load() {
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte(`{"code":"GRAPH_STORE_UNAVAILABLE","message":"unsafe provider detail","retryable":true,"details":{},"request_id":"provider-safe"}`))
			return
		}
		_, _ = writer.Write([]byte(compatibleHealth))
	}))
	defer server.Close()

	settings := runtimeconfig.Default()
	settings.Graph.Mode = runtimeconfig.GraphExternal
	settings.Graph.Endpoint = server.URL
	startup := &settingsStartup{current: settings}
	dependency := &graphRuntimeDependency{settings: startup, root: t.TempDir()}
	if err := dependency.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	process, health := dependency.Snapshot()
	if process.Ownership != platformprocess.OwnershipExternal || process.State != graphprocess.StateExternal || process.Endpoint != server.URL || health.State != graphprocess.HealthDegraded {
		t.Fatalf("process=%#v health=%#v", process, health)
	}
	if preconditions := dependency.RuntimeActionPreconditions(); !preconditions["dependency_observed"] || !preconditions["graph_configured"] {
		t.Fatalf("preconditions=%v", preconditions)
	}

	unavailable.Store(true)
	if err := dependency.Reprobe(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, health = dependency.Snapshot()
	if health.State != graphprocess.HealthUnavailable || health.Reason != "GRAPH_STORE_UNAVAILABLE" || health.Generation < 2 {
		t.Fatalf("health=%#v", health)
	}

	unavailable.Store(false)
	if err := dependency.Reconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	process, health = dependency.Snapshot()
	if process.Ownership != platformprocess.OwnershipExternal || health.State != graphprocess.HealthDegraded {
		t.Fatalf("reconnected process=%#v health=%#v", process, health)
	}
	if err := dependency.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if response, err := http.Get(server.URL + "/health"); err != nil {
		t.Fatalf("external service was stopped: %v", err)
	} else {
		_ = response.Body.Close()
	}
}

func TestGraphRuntimeDependencyCloseCancelsAndJoinsRefreshWorker(t *testing.T) {
	settings := runtimeconfig.Default()
	startup := &settingsStartup{current: settings}
	started := make(chan struct{})
	stopped := make(chan struct{})
	dependency := &graphRuntimeDependency{
		settings: startup,
		refresh: func(ctx context.Context) {
			close(started)
			<-ctx.Done()
			close(stopped)
		},
	}
	if err := dependency.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh worker did not start")
	}
	if err := dependency.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("refresh worker was not joined before close returned")
	}
}
