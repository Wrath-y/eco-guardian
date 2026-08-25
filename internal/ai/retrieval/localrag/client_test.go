package localrag

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
)

func TestClientSendsExplicitFrozenSnapshotFiltersAndRegisteredLimits(t *testing.T) {
	responseBody, err := os.ReadFile("../../../../tests/contract/fixtures/local-rag-hybrid-graph-retrieval-v1/hybrid-response.json")
	if err != nil {
		t.Fatal(err)
	}
	var captured wireRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/prefix/v1/graphs/018f9e40-0000-7000-8000-000000000201/retrieve" || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request=%s %s headers=%v", request.Method, request.URL.Path, request.Header)
		}
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&captured); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(responseBody)
	}))
	defer server.Close()
	client, err := New(server.URL+"/prefix", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	request := localRAGRequest()
	response, err := client.Retrieve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if captured.SnapshotVersion != request.Base.GraphSnapshot || len(captured.RelationshipKinds) != 1 || captured.RelationshipKinds[0] != "explicit" {
		t.Fatalf("captured identity=%#v", captured)
	}
	if len(captured.NodeTypes) != 1 || captured.NodeTypes[0] != "kind" || len(captured.EdgeTypes) != 1 || captured.EdgeTypes[0] != "relation" {
		t.Fatalf("captured filters=%#v", captured)
	}
	if captured.SeedLimit != request.Budget.RetrievalSeedLimit || captured.ResultLimit != request.Budget.RetrievalResultLimit || captured.GraphDepth != request.Budget.RetrievalGraphDepth {
		t.Fatalf("captured limits=%#v", captured)
	}
	if response.Request.Query != request.Query || response.ResolvedSnapshotVersion != "candidate" || response.ModeUsed != "hybrid" || len(response.Results) != 1 || response.Results[0].Node.ID != "alpha" {
		t.Fatalf("response=%#v", response)
	}
}

func TestClientPreservesStableIndexNotReadyWithoutRebuild(t *testing.T) {
	body, err := os.ReadFile("../../../../tests/contract/fixtures/local-rag-hybrid-graph-retrieval-v1/evicted-error.json")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write(body)
	}))
	defer server.Close()
	client, _ := New(server.URL, server.Client())
	_, err = client.Retrieve(context.Background(), localRAGRequest())
	var dependency *DependencyError
	if !errors.As(err, &dependency) || dependency.Code != "SNAPSHOT_INDEX_NOT_READY" || dependency.Retryable || !dependency.RebuildRequired || calls != 1 {
		t.Fatalf("error=%#v calls=%d", err, calls)
	}
}

func TestClientRejectsMixedIdentityFiltersAndGenerationsBeforeExposure(t *testing.T) {
	original, err := os.ReadFile("../../../../tests/contract/fixtures/local-rag-hybrid-graph-retrieval-v1/hybrid-response.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   error
	}{
		{"snapshot", func(value map[string]any) { value["resolved_snapshot_version"] = "active" }, retrieval.ErrSnapshotIdentityMismatch},
		{"content hash", func(value map[string]any) { value["content_hash"] = strings.Repeat("b", 64) }, retrieval.ErrSnapshotIdentityMismatch},
		{"node filter", func(value map[string]any) {
			value["results"].([]any)[0].(map[string]any)["node"].(map[string]any)["type"] = "other"
		}, retrieval.ErrResponseFilterMismatch},
		{"generation", func(value map[string]any) { delete(value, "vector_generation") }, retrieval.ErrResponseInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(original, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			body, _ := json.Marshal(value)
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write(body) }))
			defer server.Close()
			client, _ := New(server.URL, server.Client())
			response, err := client.Retrieve(context.Background(), localRAGRequest())
			if !errors.Is(err, test.want) || len(response.Results) != 0 {
				t.Fatalf("response=%#v err=%v want=%v", response, err, test.want)
			}
		})
	}
}

func localRAGRequest() retrieval.Request {
	hash := aicontract.Hash(strings.Repeat("a", 64))
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000201")
	revisionID := aicontract.RevisionID("candidate")
	fixture := aicontract.V1Fixture()
	budget := aicontract.Budget{Policy: fixture.Budget.Identity, BudgetLimits: fixture.Budget.Limits}
	return retrieval.Request{
		Base:  aicontract.FrozenBaseIdentity{ProjectID: projectID, ConfigRevisionID: revisionID, ConfigHash: hash, VersionManifestHash: hash, MaterializationHash: hash, GraphNamespace: string(projectID), GraphSnapshot: string(revisionID), GraphContentHash: hash},
		Query: "find alpha", Filters: retrieval.Filters{NodeTypes: []string{"kind"}, EdgeTypes: []string{"relation"}}, Budget: budget,
	}
}
