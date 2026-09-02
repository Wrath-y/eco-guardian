package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/platform/appdir"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type graphRuntimeFixture struct {
	mu        sync.Mutex
	namespace string
	version   string
	hash      string
	nodes     int
	edges     int
}

func (fixture *graphRuntimeFixture) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	if request.URL.Path == "/health" {
		_, _ = writer.Write([]byte(`{"schema_version":"1.0","status":"ok","service":"local-rag","service_version":"9.0.0","api_versions":["v1"],"supported_schema_versions":["1.0"],"capabilities":[{"name":"snapshot_lifecycle","state":"available"},{"name":"task_polling","state":"available"}],"dependencies":[{"name":"sqlite","state":"available"},{"name":"graph_migrations","state":"available"},{"name":"core_graph_query","state":"available"},{"name":"bm25","state":"available"},{"name":"vector","state":"available"},{"name":"rerank","state":"available"}],"limits":[{"name":"rebuild_components","value":3}]}`))
		return
	}
	if request.URL.Path == "/v1/tasks/graph-task" {
		fixture.mu.Lock()
		namespace, version := fixture.namespace, fixture.version
		fixture.mu.Unlock()
		_, _ = fmt.Fprintf(writer, `{"task_id":"graph-task","operation":"snapshot_build","namespace":%q,"snapshot_version":%q,"state":"succeeded","phase":"completed","progress":1,"warnings":[],"created_at":"2026-09-02T00:00:00Z"}`, namespace, version)
		return
	}
	parts := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(parts) == 6 && parts[0] == "v1" && parts[1] == "graphs" && parts[3] == "snapshots" {
		namespace, version := parts[2], parts[4]
		if parts[5] == "activate" && request.Method == http.MethodPost {
			_, _ = fmt.Fprintf(writer, `{"namespace":%q,"active_version":%q,"changed":true}`, namespace, version)
			return
		}
	}
	if len(parts) == 5 && parts[0] == "v1" && parts[1] == "graphs" && parts[3] == "snapshots" {
		namespace, version := parts[2], parts[4]
		switch request.Method {
		case http.MethodPut:
			var payload graphsync.PutSnapshotRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			fixture.mu.Lock()
			fixture.namespace, fixture.version, fixture.hash = namespace, version, payload.ContentHash
			fixture.nodes, fixture.edges = len(payload.Nodes), len(payload.Edges)
			fixture.mu.Unlock()
			writer.WriteHeader(http.StatusAccepted)
			fixture.writeSnapshot(writer, "building", false)
			return
		case http.MethodGet:
			fixture.writeSnapshot(writer, "ready", true)
			return
		}
	}
	writer.WriteHeader(http.StatusNotFound)
}

func (fixture *graphRuntimeFixture) writeSnapshot(writer http.ResponseWriter, status string, queryReady bool) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	_, _ = fmt.Fprintf(writer, `{"namespace":%q,"version":%q,"base_version":null,"schema_version":"1.0","content_hash":%q,"node_count":%d,"edge_count":%d,"task_id":"graph-task","status":%q,"query_ready":%t,"components":[{"name":"graph","state":"ready"},{"name":"fts","state":"ready"},{"name":"vector","state":"ready"}],"warnings":[]}`, fixture.namespace, fixture.version, fixture.hash, fixture.nodes, fixture.edges, status, queryReady)
}

func TestExternalGraphRuntimeCompletesProjectionAndEnablesCapabilityChain(t *testing.T) {
	provider := httptest.NewServer(&graphRuntimeFixture{})
	defer provider.Close()
	exerciseExternalGraphRuntime(t, provider.URL, 8*time.Second)
}

func TestExternalGraphRuntimeAgainstLiveProvider(t *testing.T) {
	endpoint := os.Getenv("ECO_GRAPH_E2E_ENDPOINT")
	if endpoint == "" {
		t.Skip("set ECO_GRAPH_E2E_ENDPOINT to run against local-rag")
	}
	exerciseExternalGraphRuntime(t, endpoint, 5*time.Minute)
}

func exerciseExternalGraphRuntime(t *testing.T, endpoint string, timeout time.Duration) {
	t.Helper()
	paths, err := appdir.ResolveWindows(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(t.TempDir(), "project")
	if err = os.MkdirAll(projectPath, 0o700); err != nil {
		t.Fatal(err)
	}
	process, err := Build(BuildOptions{
		Args:        []string{"--browser-auto-open", "false", "--graph-endpoint", endpoint},
		Environment: runtimeconfig.EnvironmentMap{}, Paths: paths,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close(context.Background())
	token, _, err := process.Projects.IssueSelection(context.Background(), pathSelector(projectPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = process.Projects.Create(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	handle, ok := process.Projects.ActiveHandle()
	if !ok {
		t.Fatal("project handle is unavailable")
	}
	storeProvider := handle.(interface{ Store() *store.Store })
	projectStore := storeProvider.Store()
	_, revision, err := projectStore.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "graph_ready", Name: "Graph Ready", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = process.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, found, stateErr := projectStore.GetGraphSyncState(context.Background(), revision.ID)
		if stateErr == nil && found && state.Pipeline == graphsync.StateFailed {
			job, _ := projectStore.GetGraphJob(context.Background(), domain.ID(state.LatestJobID))
			events, _ := projectStore.ListGraphJobEvents(context.Background(), domain.ID(state.LatestJobID), 0)
			t.Fatalf("Graph projection failed: state=%#v job=%#v events=%#v", state, job, events)
		}
		if stateErr == nil && found && state.Pipeline == graphsync.StateReady {
			response, getErr := http.Get(process.Host.URL() + "/api/v1/runtime/status")
			if getErr != nil {
				t.Fatal(getErr)
			}
			var status struct {
				Capabilities []struct{ ID, State string }
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&status)
			_ = response.Body.Close()
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			states := map[string]string{}
			for _, capability := range status.Capabilities {
				states[capability.ID] = capability.State
			}
			for _, capabilityID := range []string{"graph.sync", "impact.deterministic", "retrieval", "release"} {
				if states[capabilityID] != "available" {
					t.Fatalf("capability %s=%s; all=%v", capabilityID, states[capabilityID], states)
				}
			}
			assertReleasePreflightIsWired(t, process.Host.URL(), projectStore, revision.ID)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	state, _, _ := projectStore.GetGraphSyncState(context.Background(), revision.ID)
	t.Fatalf("Graph projection did not become ready: %#v", state)
}

func assertReleasePreflightIsWired(t *testing.T, baseURL string, projectStore *store.Store, revisionID domain.ID) {
	t.Helper()
	record, err := projectStore.GetRevisionRecord(context.Background(), revisionID)
	if err != nil {
		t.Fatal(err)
	}
	policies, err := projectStore.ListPolicies(context.Background(), "", 1)
	if err != nil || len(policies.Items) != 1 {
		t.Fatalf("policies=%#v err=%v", policies, err)
	}
	payload, _ := json.Marshal(map[string]any{
		"candidate_revision_id": revisionID, "config_hash": record.Metadata.ConfigHash,
		"version_manifest_hash": record.Metadata.ManifestHash, "policy_id": policies.Items[0].ID,
		"expected_baseline_release_id": nil,
		"confirmations":                []map[string]any{{"kind": "establish_baseline", "confirmed": true}},
	})
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/releases", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "release-preflight-without-evidence")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("release worker was not reached through preflight: status=%d", response.StatusCode)
	}
}
