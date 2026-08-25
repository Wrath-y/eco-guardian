package contract_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval/localrag"
)

func TestAILocalRAGConsumerModesDegradationAndActivePointerIsolation(t *testing.T) {
	hybrid := readAIConsumerFixture(t, "hybrid-response.json")
	bm25 := readAIConsumerFixture(t, "rebuild-restored-response.json")
	vector := mutateAIConsumerJSON(t, hybrid, func(value map[string]any) {
		value["mode_used"], value["degraded"] = "vector_only", true
		delete(value, "fts_generation")
		value["warnings"] = []any{map[string]any{"stage": "bm25", "code": "BM25_UNAVAILABLE", "message": "BM25 retrieval is unavailable", "retryable": false}}
	})
	rerank := mutateAIConsumerJSON(t, hybrid, func(value map[string]any) {
		value["degraded"], value["rerank"] = true, "transient_failure"
		value["warnings"] = []any{map[string]any{"stage": "rerank", "code": "RERANK_UNAVAILABLE", "message": "Rerank is temporarily unavailable", "retryable": true}}
	})
	tests := []struct {
		name     string
		body     []byte
		mode     string
		degraded bool
		empty    bool
	}{
		{"explicit inactive ready hybrid", hybrid, "hybrid", false, false},
		{"bm25 only empty", bm25, "bm25_only", true, true},
		{"vector only", vector, "vector_only", true, false},
		{"rerank degradation", rerank, "hybrid", true, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			activeSnapshot := "active-before"
			var captured struct {
				SnapshotVersion   string   `json:"snapshot_version"`
				RelationshipKinds []string `json:"relationship_kinds"`
				SeedLimit         int      `json:"seed_limit"`
				ResultLimit       int      `json:"result_limit"`
				GraphDepth        int      `json:"graph_depth"`
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
					t.Error(err)
				}
				activeSnapshot = "active-after"
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write(test.body)
			}))
			defer server.Close()
			client, _ := localrag.New(server.URL, server.Client())
			response, err := client.Retrieve(context.Background(), aiConsumerRequest())
			if err != nil {
				t.Fatal(err)
			}
			if activeSnapshot != "active-after" || captured.SnapshotVersion != "candidate" || response.ResolvedSnapshotVersion != "candidate" {
				t.Fatalf("active=%q captured=%#v response=%#v", activeSnapshot, captured, response)
			}
			if len(captured.RelationshipKinds) != 1 || captured.RelationshipKinds[0] != "explicit" || captured.SeedLimit != 20 || captured.ResultLimit != 20 || captured.GraphDepth != 1 {
				t.Fatalf("request contract=%#v", captured)
			}
			if response.ModeUsed != test.mode || response.Degraded != test.degraded || (len(response.Results) == 0) != test.empty {
				t.Fatalf("response mode=%s degraded=%v results=%d", response.ModeUsed, response.Degraded, len(response.Results))
			}
		})
	}
}

func TestAILocalRAGConsumerUnavailableEvictionAndIdentityFailures(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     []byte
		code     string
		retry    bool
		rebuild  bool
		identity error
	}{
		{"transient unavailable", 503, []byte(`{"code":"RETRIEVAL_UNAVAILABLE","message":"temporarily unavailable","retryable":true,"request_id":"fixture"}`), "RETRIEVAL_UNAVAILABLE", true, false, nil},
		{"permanent unavailable", 503, []byte(`{"code":"RETRIEVAL_UNAVAILABLE","message":"unavailable","retryable":false,"request_id":"fixture"}`), "RETRIEVAL_UNAVAILABLE", false, false, nil},
		{"evicted", 409, readAIConsumerFixture(t, "evicted-error.json"), "SNAPSHOT_INDEX_NOT_READY", false, true, nil},
		{"snapshot mismatch", 200, mutateAIConsumerJSON(t, readAIConsumerFixture(t, "hybrid-response.json"), func(value map[string]any) { value["resolved_snapshot_version"] = "active" }), "", false, false, retrieval.ErrSnapshotIdentityMismatch},
		{"hash mismatch", 200, mutateAIConsumerJSON(t, readAIConsumerFixture(t, "hybrid-response.json"), func(value map[string]any) { value["content_hash"] = strings.Repeat("b", 64) }), "", false, false, retrieval.ErrSnapshotIdentityMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = writer.Write(test.body)
			}))
			defer server.Close()
			client, _ := localrag.New(server.URL, server.Client())
			response, err := client.Retrieve(context.Background(), aiConsumerRequest())
			if test.identity != nil {
				if !errors.Is(err, test.identity) || len(response.Results) != 0 {
					t.Fatalf("response=%#v err=%v", response, err)
				}
				return
			}
			var dependency *localrag.DependencyError
			if !errors.As(err, &dependency) || dependency.Code != test.code || dependency.Retryable != test.retry || dependency.RebuildRequired != test.rebuild || len(response.Results) != 0 {
				t.Fatalf("response=%#v dependency=%#v err=%v", response, dependency, err)
			}
		})
	}
}

func TestAILocalRAGConsumerRejectsInferredAndHardLimitExpansion(t *testing.T) {
	inferred := mutateAIConsumerJSON(t, readAIConsumerFixture(t, "hybrid-response.json"), func(value map[string]any) {
		edge := map[string]any{"id": "edge-1", "from": "alpha", "to": "beta", "type": "relation", "relation_kind": "inferred", "confidence": 0.5, "properties": map[string]any{}, "provenance": map[string]any{}}
		beta := map[string]any{"id": "beta", "type": "kind", "label": "Beta", "text": "beta", "properties": map[string]any{}, "provenance": map[string]any{}}
		result := value["results"].([]any)[0].(map[string]any)
		result["hop_count"] = 1
		path := result["evidence"].(map[string]any)["path"].(map[string]any)
		path["node_ids"], path["edge_ids"] = []any{"alpha", "beta"}, []any{"edge-1"}
		path["nodes"], path["edges"] = []any{result["node"], beta}, []any{edge}
		path["explicit_edges"], path["inferred_edges"] = []any{}, []any{edge}
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(inferred) }))
	client, _ := localrag.New(server.URL, server.Client())
	_, err := client.Retrieve(context.Background(), aiConsumerRequest())
	server.Close()
	if !errors.Is(err, retrieval.ErrResponseFilterMismatch) {
		t.Fatalf("inferred err=%v", err)
	}

	calls := 0
	limitServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer limitServer.Close()
	limitClient, _ := localrag.New(limitServer.URL, limitServer.Client())
	for _, mutate := range []func(*retrieval.Request){
		func(request *retrieval.Request) { request.Budget.RetrievalSeedLimit = 101 },
		func(request *retrieval.Request) { request.Budget.RetrievalResultLimit = 101 },
		func(request *retrieval.Request) { request.Budget.RetrievalGraphDepth = 4 },
		func(request *retrieval.Request) {
			request.Filters.NodeTypes = make([]string, retrieval.MaxFilterItems+1)
			for index := range request.Filters.NodeTypes {
				request.Filters.NodeTypes[index] = "kind-" + string(rune(index+32))
			}
		},
	} {
		request := aiConsumerRequest()
		mutate(&request)
		if _, err = limitClient.Retrieve(context.Background(), request); !errors.Is(err, localrag.ErrRequest) {
			t.Fatalf("limit err=%v", err)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid hard limits reached dependency %d times", calls)
	}
}

func aiConsumerRequest() retrieval.Request {
	hash := aicontract.Hash(strings.Repeat("a", 64))
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000201")
	fixture := aicontract.V1Fixture()
	return retrieval.Request{
		Base:  aicontract.FrozenBaseIdentity{ProjectID: projectID, ConfigRevisionID: "candidate", ConfigHash: hash, VersionManifestHash: hash, MaterializationHash: hash, GraphNamespace: string(projectID), GraphSnapshot: "candidate", GraphContentHash: hash},
		Query: "find alpha", Filters: retrieval.Filters{NodeTypes: []string{"kind"}, EdgeTypes: []string{"relation"}},
		Budget: aicontract.Budget{Policy: fixture.Budget.Identity, BudgetLimits: fixture.Budget.Limits},
	}
}

func readAIConsumerFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("fixtures", localRAGRetrievalFixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func mutateAIConsumerJSON(t *testing.T, body []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	mutate(value)
	changed, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return changed
}
