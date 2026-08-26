package contract_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLocalRAGImpactQueryContractIsPinned(t *testing.T) {
	const root = "fixtures/local-rag-impact-query-v1/"
	manifestBytes, err := os.ReadFile(root + "manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Contract string         `json:"contract"`
		Errors   []string       `json:"errors"`
		Limits   map[string]int `json:"limits"`
	}
	if err = json.Unmarshal(manifestBytes, &manifest); err != nil || manifest.Contract != "local-rag-impact-query-v1" || manifest.Limits["max_depth"] != 6 || manifest.Limits["max_paths"] != 100 {
		t.Fatalf("manifest=%#v err=%v", manifest, err)
	}
	openapi, err := os.ReadFile(root + "openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"/traverse:", "/paths:", "/retrieve:", "resolved_snapshot_version", "truncation_reasons", "SNAPSHOT_INDEX_NOT_READY"} {
		if !strings.Contains(string(openapi), token) && token != "SNAPSHOT_INDEX_NOT_READY" {
			t.Fatalf("OpenAPI missing %q", token)
		}
	}
}

func TestLocalRAGImpactQueryCasesCoverProviderBoundaries(t *testing.T) {
	data, err := os.ReadFile("fixtures/local-rag-impact-query-v1/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			ID, Operation, Expected string
		} `json:"cases"`
	}
	if err = json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, item := range fixture.Cases {
		if item.ID == "" || item.Operation == "" || item.Expected == "" || seen[item.ID] {
			t.Fatalf("invalid contract case %#v", item)
		}
		seen[item.ID] = true
	}
	for _, required := range []string{"inactive-explicit-snapshot", "incoming-stored-orientation", "multi-source-shortest-path", "max-depth-prefix", "max-nodes-prefix", "max-paths-prefix", "full-field-provenance", "hybrid-retrieve", "retrieval-index-evicted", "store-unavailable"} {
		if !seen[required] {
			t.Fatalf("missing contract case %s", required)
		}
	}
}
