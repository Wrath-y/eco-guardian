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

const localRAGOperabilityOpenAPISHA256 = "fd39c71846e49f0f6a7b4b1dc69a089634006af002d36af58c61444611df9369"

func TestLocalRAGOperabilityFixtureManifest(t *testing.T) {
	dir := filepath.Join("fixtures", "local-rag-graph-service-operability-v1")
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		FixtureVersion string            `json:"fixture_version"`
		Algorithm      string            `json:"algorithm"`
		Files          map[string]string `json:"files"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.FixtureVersion != "1.0" || manifest.Algorithm != "sha256" || len(manifest.Files) == 0 {
		t.Fatalf("invalid local-rag fixture manifest: %#v", manifest)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "manifest.json" || entry.Name() == "README.md" {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) != len(manifest.Files) {
		t.Fatalf("manifest files=%d, fixture files=%d", len(manifest.Files), len(names))
	}
	for _, name := range names {
		want, ok := manifest.Files[name]
		if !ok {
			t.Fatalf("fixture %s is not in manifest", name)
		}
		contents, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		actual := sha256.Sum256(contents)
		if got := hex.EncodeToString(actual[:]); got != want {
			t.Fatalf("fixture %s digest=%s, want %s", name, got, want)
		}
	}
}

func TestLocalRAGOperabilityContractShape(t *testing.T) {
	if localRAGOperabilityOpenAPISHA256 == "" {
		t.Fatal("local-rag OpenAPI digest must pin the vendored fixture contract")
	}
	dir := filepath.Join("fixtures", "local-rag-graph-service-operability-v1")
	read := func(name string, into any) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, into); err != nil {
			t.Fatal(err)
		}
	}

	var health map[string]struct {
		SchemaVersion           string   `json:"schema_version"`
		Status                  string   `json:"status"`
		APIVersions             []string `json:"api_versions"`
		SupportedSchemaVersions []string `json:"supported_schema_versions"`
	}
	read("health.json", &health)
	for name, wantStatus := range map[string]string{"ok": "ok", "degraded": "degraded", "unavailable": "unavailable"} {
		got, ok := health[name]
		if !ok || got.SchemaVersion != "1.0" || got.Status != wantStatus || !contains(got.APIVersions, "v1") || !contains(got.SupportedSchemaVersions, "1.0") {
			t.Fatalf("health %s does not advertise the required v1/schema-1.0 contract: %#v", name, got)
		}
	}

	var tasks map[string]struct {
		State string `json:"state"`
		Error struct {
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	read("task.json", &tasks)
	for name, want := range map[string]string{"running": "running", "succeeded": "succeeded", "failed": "failed"} {
		if got := tasks[name].State; got != want {
			t.Fatalf("task %s state=%q, want %q", name, got, want)
		}
	}
	if tasks["failed"].Error.RequestID == "" {
		t.Fatal("terminal provider task errors must retain a request ID")
	}

	var transcript struct {
		Steps []struct {
			Operation         string `json:"operation"`
			RebuildIsExplicit bool   `json:"rebuild_is_explicit"`
		}
		Invariants []string `json:"invariants"`
	}
	read("eco-guardian-consumer-transcript.json", &transcript)
	var restart, explicitRebuild bool
	for _, step := range transcript.Steps {
		restart = restart || step.Operation == "restart"
		explicitRebuild = explicitRebuild || step.RebuildIsExplicit
	}
	if !restart || !explicitRebuild || !contains(transcript.Invariants, "request_id_error_envelope") {
		t.Fatalf("consumer transcript is missing restart, explicit rebuild, or request-ID guarantees: %#v", transcript)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
