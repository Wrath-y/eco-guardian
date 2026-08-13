package contract_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

const localRAGSnapshotOpenAPISHA256 = "fd39c71846e49f0f6a7b4b1dc69a089634006af002d36af58c61444611df9369"

func TestLocalRAGSnapshotOpenAPIAndFixturesArePinned(t *testing.T) {
	dir := filepath.Join("fixtures", "local-rag-graph-snapshot-v1")
	openapi, err := os.ReadFile(filepath.Join(dir, "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(openapi)
	if got := hex.EncodeToString(digest[:]); got != localRAGSnapshotOpenAPISHA256 {
		t.Fatalf("OpenAPI digest=%s want %s", got, localRAGSnapshotOpenAPISHA256)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		FixtureVersion string            `json:"fixture_version"`
		Files          map[string]string `json:"files"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FixtureVersion != "1.0" || len(manifest.Files) == 0 {
		t.Fatalf("invalid fixture manifest: %#v", manifest)
	}
	names := make([]string, 0, len(manifest.Files))
	for name := range manifest.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		body, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != manifest.Files[name] {
			t.Fatalf("%s digest=%s want %s", name, got, manifest.Files[name])
		}
	}
}
