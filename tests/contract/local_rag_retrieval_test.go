package contract_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

const localRAGRetrievalFixtureDir = "local-rag-hybrid-graph-retrieval-v1"

func TestLocalRAGRetrievalProviderFixturesArePinned(t *testing.T) {
	dir := filepath.Join("fixtures", localRAGRetrievalFixtureDir)
	var manifest struct {
		FixtureVersion string            `json:"fixture_version"`
		Algorithm      string            `json:"algorithm"`
		Files          map[string]string `json:"files"`
	}
	readRetrievalFixture(t, dir, "manifest.json", &manifest)
	if manifest.FixtureVersion != "1.0" || manifest.Algorithm != "sha256" || len(manifest.Files) != 5 {
		t.Fatalf("invalid provider manifest: %#v", manifest)
	}
	for name, want := range manifest.Files {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Fatalf("%s digest=%s want %s", name, got, want)
		}
	}
}

func TestLocalRAGRetrievalConsumerContract(t *testing.T) {
	dir := filepath.Join("fixtures", localRAGRetrievalFixtureDir)
	var contract struct {
		SourceCommit  string `json:"source_commit"`
		OpenAPISHA256 string `json:"openapi_sha256"`
		Endpoint      string `json:"endpoint"`
		Namespace     string `json:"namespace"`
		Defaults      struct {
			RelationshipKinds []string `json:"relationship_kinds"`
			SeedLimit         int      `json:"seed_limit"`
			ResultLimit       int      `json:"result_limit"`
			GraphDepth        int      `json:"graph_depth"`
		} `json:"defaults"`
		Maxima struct {
			SeedLimit   int `json:"seed_limit"`
			ResultLimit int `json:"result_limit"`
			GraphDepth  int `json:"graph_depth"`
		} `json:"maxima"`
		RRFK      int      `json:"rrf_k"`
		Modes     []string `json:"modes"`
		Retention struct {
			ActiveSnapshot         bool `json:"active_snapshot"`
			RecentAcceptedVersions int  `json:"recent_accepted_versions"`
			DerivedGenerationsOnly bool `json:"derived_generations_only"`
		} `json:"retention"`
		Errors map[string]struct {
			HTTPStatus      int    `json:"http_status"`
			Code            string `json:"code"`
			Retryable       bool   `json:"retryable"`
			RebuildRequired bool   `json:"rebuild_required"`
			ImplicitRebuild bool   `json:"implicit_rebuild"`
		} `json:"errors"`
	}
	readRetrievalFixture(t, dir, "consumer-contract.json", &contract)
	if contract.SourceCommit != "cfbf6106a38bc46730217b987ca54c6b1b302f20" || contract.OpenAPISHA256 != "fd39c71846e49f0f6a7b4b1dc69a089634006af002d36af58c61444611df9369" {
		t.Fatalf("provider source is not pinned: %#v", contract)
	}
	if contract.Endpoint != "POST /v1/graphs/{namespace}/retrieve" || contract.Namespace != "project" {
		t.Fatalf("unexpected retrieval endpoint binding: %#v", contract)
	}
	if len(contract.Defaults.RelationshipKinds) != 1 || contract.Defaults.RelationshipKinds[0] != "explicit" || contract.Defaults.SeedLimit != 20 || contract.Defaults.ResultLimit != 20 || contract.Defaults.GraphDepth != 1 {
		t.Fatalf("unexpected retrieval defaults: %#v", contract.Defaults)
	}
	if contract.Maxima.SeedLimit != 100 || contract.Maxima.ResultLimit != 100 || contract.Maxima.GraphDepth != 3 || contract.RRFK != 60 {
		t.Fatalf("unexpected retrieval limits/scoring: %#v", contract)
	}
	if len(contract.Modes) != 3 || !contract.Retention.ActiveSnapshot || contract.Retention.RecentAcceptedVersions != 20 || !contract.Retention.DerivedGenerationsOnly {
		t.Fatalf("unexpected modes or retention contract: %#v", contract)
	}
	evicted := contract.Errors["evicted"]
	if evicted.HTTPStatus != 409 || evicted.Code != "SNAPSHOT_INDEX_NOT_READY" || evicted.Retryable || !evicted.RebuildRequired || evicted.ImplicitRebuild {
		t.Fatalf("unexpected evicted-index contract: %#v", evicted)
	}
	for name, wantRetryable := range map[string]bool{"both_base_unavailable_transient": true, "both_base_unavailable_permanent": false} {
		got := contract.Errors[name]
		if got.HTTPStatus != 503 || got.Code != "RETRIEVAL_UNAVAILABLE" || got.Retryable != wantRetryable {
			t.Fatalf("unexpected %s contract: %#v", name, got)
		}
	}

	var request struct {
		Query             string   `json:"query"`
		SnapshotVersion   string   `json:"snapshot_version"`
		RelationshipKinds []string `json:"relationship_kinds"`
		SeedLimit         int      `json:"seed_limit"`
		ResultLimit       int      `json:"result_limit"`
		GraphDepth        int      `json:"graph_depth"`
	}
	readRetrievalFixture(t, dir, "retrieve-request.json", &request)
	if request.Query != "find alpha" || request.SnapshotVersion != "candidate" || len(request.RelationshipKinds) != 1 || request.RelationshipKinds[0] != "explicit" || request.SeedLimit != 20 || request.ResultLimit != 20 || request.GraphDepth != 1 {
		t.Fatalf("provider request drifted: %#v", request)
	}

	var response retrievalFixtureResponse
	readRetrievalFixture(t, dir, "hybrid-response.json", &response)
	if response.ResolvedSnapshotVersion != request.SnapshotVersion || len(response.ContentHash) != 64 || response.ModeUsed != "hybrid" || response.Degraded || response.Rerank != "skipped" {
		t.Fatalf("provider response identity/mode drifted: %#v", response)
	}
	if response.FTSGeneration.Algorithm != "graph-search-v1/fts5" || response.FTSGeneration.Tokenizer != "unicode61" || response.VectorGeneration.Algorithm != "graph-search-v1/embedding" || response.VectorGeneration.Provider != "fake" || response.VectorGeneration.Model != "fixture" || response.VectorGeneration.Dimensions != 2 {
		t.Fatalf("generation/model identity drifted: %#v %#v", response.FTSGeneration, response.VectorGeneration)
	}
	if len(response.Results) != 1 || response.Results[0].CitationText != "alpha" || response.Results[0].Evidence.Seed.NodeID != "alpha" || len(response.Results[0].Evidence.Path.NodeIDs) != 1 {
		t.Fatalf("citation/evidence drifted: %#v", response.Results)
	}
	wantRRF := 2.0 / float64(contract.RRFK+1)
	if math.Abs(response.Results[0].Scores.RRFScore-wantRRF) > 1e-15 || response.Results[0].Scores.GraphScore != response.Results[0].Scores.RRFScore {
		t.Fatalf("RRF/graph score drifted: %#v", response.Results[0].Scores)
	}

	var rebuilt retrievalFixtureResponse
	readRetrievalFixture(t, dir, "rebuild-restored-response.json", &rebuilt)
	if rebuilt.ModeUsed != "bm25_only" || !rebuilt.Degraded || len(rebuilt.Warnings) != 1 || rebuilt.Warnings[0].Stage != "vector" || rebuilt.Warnings[0].Code != "VECTOR_UNAVAILABLE" || rebuilt.FTSGeneration.Generation != "fts-rebuilt" {
		t.Fatalf("degradation/rebuild response drifted: %#v", rebuilt)
	}

	var providerError struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
		Details   struct {
			RebuildRequired bool `json:"rebuild_required"`
		} `json:"details"`
		RequestID string `json:"request_id"`
	}
	readRetrievalFixture(t, dir, "evicted-error.json", &providerError)
	if providerError.Code != evicted.Code || providerError.Retryable || !providerError.Details.RebuildRequired || providerError.RequestID == "" {
		t.Fatalf("provider error drifted: %#v", providerError)
	}
}

type retrievalFixtureGeneration struct {
	Component     string `json:"component"`
	Generation    string `json:"generation"`
	Algorithm     string `json:"algorithm"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Dimensions    int    `json:"dimensions"`
	Tokenizer     string `json:"tokenizer"`
	ContentDigest string `json:"content_digest"`
}

type retrievalFixtureResponse struct {
	ResolvedSnapshotVersion string                     `json:"resolved_snapshot_version"`
	ContentHash             string                     `json:"content_hash"`
	ModeUsed                string                     `json:"mode_used"`
	Degraded                bool                       `json:"degraded"`
	Rerank                  string                     `json:"rerank"`
	FTSGeneration           retrievalFixtureGeneration `json:"fts_generation"`
	VectorGeneration        retrievalFixtureGeneration `json:"vector_generation"`
	Warnings                []struct {
		Stage string `json:"stage"`
		Code  string `json:"code"`
	} `json:"warnings"`
	Results []struct {
		CitationText string `json:"citation_text"`
		Scores       struct {
			RRFScore   float64 `json:"rrf_score"`
			GraphScore float64 `json:"graph_score"`
		} `json:"scores"`
		Evidence struct {
			Seed struct {
				NodeID string `json:"node_id"`
			} `json:"seed"`
			Path struct {
				NodeIDs []string `json:"node_ids"`
			} `json:"path"`
		} `json:"evidence"`
	} `json:"results"`
}

func readRetrievalFixture(t *testing.T, dir, name string, into any) {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatal(err)
	}
}
