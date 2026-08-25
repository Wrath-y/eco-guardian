package contract

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"testing"
)

func TestCanonicalInputAndRegistryIdentityIgnoreOrdering(t *testing.T) {
	base := validInput()
	base.Goals = append(base.Goals, Goal{ID: "goal-2", Description: "Preserve pacing"})
	base.Constraints = append(base.Constraints, Constraint{ID: "constraint-2", Path: "/stats/speed", Operator: ConstraintGreaterEqual, Value: json.RawMessage(`1`)})
	base.Scenes = append(base.Scenes, "scene-secondary")
	baseHash, err := HashAIDesignInputV1(base)
	if err != nil {
		t.Fatal(err)
	}
	tools := V1Fixture().Tools
	for seed := int64(0); seed < 64; seed++ {
		random := rand.New(rand.NewSource(seed))
		candidate := base
		candidate.Goals = shuffled(random, base.Goals)
		candidate.Constraints = shuffled(random, base.Constraints)
		candidate.Scenes = shuffled(random, base.Scenes)
		candidate.RequiredVersions = shuffled(random, base.RequiredVersions)
		hash, err := HashAIDesignInputV1(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if hash != baseHash {
			t.Fatalf("seed %d changed input identity: %s != %s", seed, hash, baseHash)
		}
		registry, err := NewAIToolRegistry(shuffled(random, tools))
		if err != nil {
			t.Fatal(err)
		}
		for _, tool := range tools {
			resolved, ok := registry.Resolve(tool.Identity.ID, tool.Identity.Version)
			if !ok || resolved.Identity.Hash != tool.Identity.Hash {
				t.Fatalf("seed %d changed registration identity for %s", seed, tool.Identity.ID)
			}
		}
	}
}

func TestPresentationMetadataCannotEnterInputIdentity(t *testing.T) {
	input := validInput()
	canonical, err := CanonicalAIDesignInputV1(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("display_name"), []byte("job_id"), []byte("timestamp"), []byte("worker_id")} {
		if bytes.Contains(canonical, forbidden) {
			t.Fatalf("presentation/runtime field %q entered canonical input", forbidden)
		}
	}
	metadataVariants := []struct {
		DisplayName string
		JobID       string
		Timestamp   string
	}{
		{DisplayName: "Alpha", JobID: "job-1", Timestamp: "2026-01-01T00:00:00Z"},
		{DisplayName: "Renamed", JobID: "job-999", Timestamp: "2030-12-31T23:59:59Z"},
	}
	want, err := HashAIDesignInputV1(input)
	if err != nil {
		t.Fatal(err)
	}
	for range metadataVariants {
		got, err := HashAIDesignInputV1(input)
		if err != nil || got != want {
			t.Fatalf("presentation metadata altered input identity: %s != %s (%v)", got, want, err)
		}
	}
}

func TestEveryRecordedVersionDimensionChangesCanonicalIdentity(t *testing.T) {
	provider := ProviderManifest{Identity: version("provider"), ProviderID: "openai-compatible", EndpointClassification: EndpointLoopback, Model: "model-a", Capabilities: []string{"structured_output"}, Parameters: json.RawMessage(`{"temperature":0}`)}
	prompt := PromptManifest{Identity: version("prompt"), Template: "Return DraftPatchV1."}
	schema := SchemaManifest{Identity: version("schema"), Schema: json.RawMessage(`{"type":"object"}`)}
	tool := validToolManifest("preview_simulation")
	input := validInput()
	preview := Preview{Advisory: true, InputHash: testHash, ResultHash: testHash, Evaluators: []VersionIdentity{version("simulation")}, Evidence: []EvidenceRef{{ID: "evidence-1", Kind: EvidenceRetrieval, ManifestHash: testHash}}, Acceptable: true}

	assertChanged(t, "model", func() (Hash, error) { return HashProviderManifest(provider) }, func() (Hash, error) {
		changed := provider
		changed.Model = "model-b"
		return HashProviderManifest(changed)
	})
	assertChanged(t, "prompt", func() (Hash, error) { return HashPromptManifest(prompt) }, func() (Hash, error) {
		changed := prompt
		changed.Identity.Version = "v2"
		return HashPromptManifest(changed)
	})
	assertChanged(t, "schema", func() (Hash, error) { return HashSchemaManifest(schema) }, func() (Hash, error) {
		changed := schema
		changed.Schema = json.RawMessage(`{"type":"array"}`)
		return HashSchemaManifest(changed)
	})
	assertChanged(t, "tool", func() (Hash, error) { return HashToolManifest(tool) }, func() (Hash, error) {
		changed := tool
		changed.Identity.Version = "v2"
		return HashToolManifest(changed)
	})
	assertChanged(t, "input dependency", func() (Hash, error) { return HashAIDesignInputV1(input) }, func() (Hash, error) {
		changed := input
		changed.RequiredVersions = append([]VersionIdentity(nil), input.RequiredVersions...)
		changed.RequiredVersions[0].Version = "v2"
		return HashAIDesignInputV1(changed)
	})
	assertChanged(t, "evaluator", func() (Hash, error) { return HashPreview(preview) }, func() (Hash, error) {
		changed := preview
		changed.Evaluators = append([]VersionIdentity(nil), preview.Evaluators...)
		changed.Evaluators[0].Version = "v2"
		return HashPreview(changed)
	})
	assertChanged(t, "evidence", func() (Hash, error) { return HashPreview(preview) }, func() (Hash, error) {
		changed := preview
		changed.Evidence = append([]EvidenceRef(nil), preview.Evidence...)
		changed.Evidence[0].ManifestHash = Hash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
		return HashPreview(changed)
	})
}

func TestV1ManifestIdentityGoldens(t *testing.T) {
	fixture := V1Fixture()
	want := map[string]Hash{
		V1PromptID:              "1dbd1f20d5ff02ff0c3397154219aa468a0d39fa8a90fba94e83bbafa854d010",
		V1DraftPatchSchemaID:    "c70965d5a5380509607678cd7efcb92041b2215bc926f7c609d62ada47b15bc7",
		V1BudgetPolicyID:        "d3f3b033bdfa8a3baabd771ed291de08dad4ae5a3ce37b469f97a21e9d332db6",
		V1OrchestratorID:        "de577e79e3482ac738e3a9fd789c6d7d95b81495814d52708027dca0f88cee79",
		"read_revision_context": "a0e4446427a793611ad1e2f48c8b538149d7a04362272a75c4520226d6b2274e",
		"retrieve_evidence":     "9912623bbef0370fc7b206eeae3f8edf1044cc4b5438238d9bc4a09bf9daad89",
		"validate_proposal":     "94abbf27aa33a28625c7f308331d77e97cfd26d1621fd8713704b4019d8e8e8d",
		"preview_simulation":    "83f10c7ef08f913c28c47ade964150afc1dffb6b1843d91c6b7ed39176e669a7",
		"preview_risk":          "19c01412c5102788ced2aa526ec2bca0bb89638f875f4679e4096cbbc02708fe",
		"search_parameters":     "2f288dd2ceddb67d8d4298b71398e1940812a449f2a2f943e1878f8070b8ba38",
	}
	if fixture.Prompt.Identity.Hash != want[V1PromptID] || fixture.PatchSchema.Identity.Hash != want[V1DraftPatchSchemaID] || fixture.Budget.Identity.Hash != want[V1BudgetPolicyID] || fixture.Orchestrator.Identity.Hash != want[V1OrchestratorID] {
		t.Fatalf("v1 registry golden identity drift: %#v", fixture)
	}
	if len(fixture.Tools) != len(V1ToolNames) {
		t.Fatalf("v1 tool identity matrix=%d want=%d", len(fixture.Tools), len(V1ToolNames))
	}
	for _, tool := range fixture.Tools {
		if tool.Identity.Version != V1Version || tool.Identity.Hash != want[tool.Identity.ID] {
			t.Errorf("v1 tool identity drift: %#v", tool.Identity)
		}
	}
}

func assertChanged(t *testing.T, name string, before, after func() (Hash, error)) {
	t.Helper()
	first, err := before()
	if err != nil {
		t.Fatalf("%s before: %v", name, err)
	}
	second, err := after()
	if err != nil {
		t.Fatalf("%s after: %v", name, err)
	}
	if first == second {
		t.Fatalf("%s version/content change did not alter identity %s", name, first)
	}
}

func shuffled[T any](random *rand.Rand, values []T) []T {
	result := append([]T(nil), values...)
	random.Shuffle(len(result), func(i, j int) { result[i], result[j] = result[j], result[i] })
	return result
}
