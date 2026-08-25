package contract

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestV1RegistrySetIsClosedAndBounded(t *testing.T) {
	registry, err := NewV1RegistrySet()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range V1ToolNames {
		tool, ok := registry.Tools.Resolve(name, V1Version)
		if !ok {
			t.Fatalf("missing v1 tool %q", name)
		}
		if tool.MaxCalls <= 0 || tool.MaxResultBytes <= 0 || tool.TimeoutMillis <= 0 {
			t.Fatalf("tool %q has an unbounded limit: %#v", name, tool)
		}
	}
	if len(V1Fixture().Tools) != 6 {
		t.Fatalf("v1 tool count=%d want 6", len(V1Fixture().Tools))
	}
	retrieval, _ := registry.Tools.Resolve("retrieve_evidence", V1Version)
	if retrieval.MaxCalls != 1 || retrieval.SideEffect != "external_read_only" {
		t.Fatalf("retrieval must be a one-shot read-only tool: %#v", retrieval)
	}
	limits, err := registry.ResolvedLimits()
	if err != nil {
		t.Fatal(err)
	}
	if limits.MaxFormatRepairs != 3 || limits.MaxProviderTurns != 4 || limits.MaxToolCalls <= 0 || limits.MaxSearchCandidates <= 0 || limits.MaxDurationMillis <= 0 || limits.MaxContextBytes <= 0 || limits.MaxOutputBytes <= 0 {
		t.Fatalf("invalid resolved v1 limits: %#v", limits)
	}
	limits.MaxProviderTurns = 1_000_000
	again, err := registry.ResolvedLimits()
	if err != nil {
		t.Fatal(err)
	}
	if again.MaxProviderTurns != V1MaxProviderTurns {
		t.Fatal("resolved limits exposed mutable registry state")
	}
}

func TestV1ManifestFixtureIsPinned(t *testing.T) {
	body, err := os.ReadFile("testdata/v1-manifests.json")
	if err != nil {
		t.Fatal(err)
	}
	var pinned V1FixtureSet
	if err := json.Unmarshal(body, &pinned); err != nil {
		t.Fatal(err)
	}
	pinnedCanonical, err := canonicalJSON(pinned)
	if err != nil {
		t.Fatal(err)
	}
	builtInCanonical, err := canonicalJSON(V1Fixture())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pinnedCanonical, builtInCanonical) {
		t.Fatalf("built-in v1 manifests drifted from testdata/v1-manifests.json")
	}
}
