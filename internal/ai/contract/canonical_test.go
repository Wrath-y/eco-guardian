package contract

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestCanonicalAIDesignInputSortsSemanticSetsWithoutMutatingInput(t *testing.T) {
	first := validInput()
	first.Goals = append(first.Goals, Goal{ID: "goal-0", Description: "Preserve cost"})
	first.Scenes = []string{"scene-z", "scene-a"}
	first.AllowedTargets[0].Paths = []AllowedPath{
		{Path: "/stats/speed", Operations: []PatchOperationKind{OperationRemove, OperationAdd}},
		{Path: "/stats/damage", Operations: []PatchOperationKind{OperationReplace}},
	}
	second := first
	second.Goals = reverseCopy(first.Goals)
	second.Scenes = reverseCopy(first.Scenes)
	second.AllowedTargets = append([]AllowedTarget(nil), first.AllowedTargets...)
	second.AllowedTargets[0].Paths = reverseCopy(first.AllowedTargets[0].Paths)
	second.AllowedTargets[0].Paths[1].Operations = reverseCopy(second.AllowedTargets[0].Paths[1].Operations)

	firstJSON, err := CanonicalAIDesignInputV1(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := CanonicalAIDesignInputV1(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("semantic ordering changed canonical JSON:\n%s\n%s", firstJSON, secondJSON)
	}
	if first.Scenes[0] != "scene-z" || first.AllowedTargets[0].Paths[0].Path != "/stats/speed" {
		t.Fatal("canonicalization mutated caller-owned input")
	}
	firstHash, err := HashAIDesignInputV1(first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := HashAIDesignInputV1(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash || !firstHash.Valid() {
		t.Fatalf("input hashes differ: %s != %s", firstHash, secondHash)
	}
}

func TestCanonicalJSONSortsMapKeysAndRequiresCanonicalDecimals(t *testing.T) {
	first := SchemaManifest{Identity: version("schema"), Schema: json.RawMessage(`{"z":2,"a":{"b":1,"a":0}}`)}
	second := first
	second.Schema = json.RawMessage(` { "a": {"a":0, "b":1}, "z":2 } `)
	firstJSON, err := CanonicalSchemaManifest(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := CanonicalSchemaManifest(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatalf("map order changed canonical JSON:\n%s\n%s", firstJSON, secondJSON)
	}
	nonCanonical := first
	nonCanonical.Schema = json.RawMessage(`{"value":1.0}`)
	if _, err := CanonicalSchemaManifest(nonCanonical); err == nil {
		t.Fatal("non-canonical decimal was accepted")
	}
}

func TestCanonicalCodecsCoverEveryVersionedAIDomain(t *testing.T) {
	evidence := EvidenceRef{ID: "evidence-1", Kind: EvidenceRetrieval, ManifestHash: testHash}
	call := ToolCall{ID: "call-1", AttemptID: "attempt-1", Ordinal: 1, Tool: version("preview_simulation"), Base: validBase(), InputHash: testHash}
	result := ToolResult{CallID: call.ID, ImplementationVersion: "simulation-preview-v1", ResultHash: testHash, Evidence: []EvidenceRef{evidence}}
	stages := []AttemptStage{StageInputPinned, StageEvidencePinned, StageProviderToolLoop, StageDeterministicPreview, StagePatchSealed}
	tests := []struct {
		name string
		hash func() (Hash, error)
	}{
		{"provider manifest", func() (Hash, error) {
			return HashProviderManifest(ProviderManifest{Identity: version("provider"), ProviderID: "openai-compatible", EndpointClassification: EndpointLoopback, Model: "fixture", Capabilities: []string{"tools", "structured_output"}, Parameters: json.RawMessage(`{"temperature":0}`)})
		}},
		{"prompt manifest", func() (Hash, error) {
			return HashPromptManifest(PromptManifest{Identity: version("prompt"), Template: "Return a DraftPatchV1."})
		}},
		{"schema manifest", func() (Hash, error) {
			return HashSchemaManifest(SchemaManifest{Identity: version("schema"), Schema: json.RawMessage(`{"type":"object"}`)})
		}},
		{"tool manifest", func() (Hash, error) {
			return HashToolManifest(ToolManifest{Identity: version("tool"), InputSchemaHash: testHash, ResultSchemaHash: testHash, RequiredIdentities: []string{"base", "input"}, Deterministic: true, SideEffect: "none", MaxCalls: 2, MaxResultBytes: 4096, TimeoutMillis: 1000, CancellationBehavior: "context"})
		}},
		{"orchestrator manifest", func() (Hash, error) {
			return HashOrchestratorManifest(OrchestratorManifest{Identity: version("orchestrator"), Stages: stages})
		}},
		{"retrieval evidence", func() (Hash, error) {
			return HashRetrievalEvidenceManifest(RetrievalEvidenceManifest{Identity: version("retrieval"), Base: validBase(), RequestHash: testHash, ResponseHash: testHash, Mode: "hybrid", Evidence: []EvidenceRef{evidence}})
		}},
		{"tool input", func() (Hash, error) {
			return HashToolInput(ToolInputEnvelope{Call: call, Payload: json.RawMessage(`{"scene":"fixed"}`)})
		}},
		{"tool result", func() (Hash, error) {
			return HashToolResult(ToolResultEnvelope{Result: result, Payload: json.RawMessage(`{"advisory":true}`)})
		}},
		{"draft patch", func() (Hash, error) { return HashDraftPatch(validPatch()) }},
		{"draft diff", func() (Hash, error) {
			return HashDraftDiff(DraftDiff{PatchHash: testHash, Changes: []DraftDiffChange{{EntityID: "entity-1", Path: "/stats/damage", Ordinal: 1, Kind: OperationReplace, Original: json.RawMessage(`10`), Canonical: json.RawMessage(`12`)}}})
		}},
		{"preview", func() (Hash, error) {
			return HashPreview(Preview{Advisory: true, InputHash: testHash, ResultHash: testHash, Evaluators: []VersionIdentity{version("risk"), version("simulation")}, Evidence: []EvidenceRef{evidence}, Acceptable: true})
		}},
		{"audit", func() (Hash, error) {
			return HashAuditRecord(AuditRecord{Ordinal: 1, AttemptID: "attempt-1", EventType: "input_pinned", PayloadHash: testHash, Versions: []VersionIdentity{version("orchestrator")}})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hash, err := test.hash()
			if err != nil {
				t.Fatal(err)
			}
			if !hash.Valid() {
				t.Fatalf("invalid SHA-256 identity %q", hash)
			}
		})
	}
}

func TestHashesAreDomainSeparatedAndExcludeGeneratedPatchIdentity(t *testing.T) {
	payload := []byte(`{"same":"bytes"}`)
	inputHash, err := hashEncoded(domainAIDesignInput, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	previewHash, err := hashEncoded(domainPreview, payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inputHash == previewHash {
		t.Fatal("different contract domains produced the same hash")
	}
	first := validPatch()
	second := first
	second.ID = "patch-generated-later"
	second.Hash = Hash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	firstHash, err := HashDraftPatch(first)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := HashDraftPatch(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("generated patch identity altered content hash: %s != %s", firstHash, secondHash)
	}
}

func TestAuditChainPinsOrderAndPreviousHash(t *testing.T) {
	firstRecord := AuditRecord{Ordinal: 1, AttemptID: "attempt-1", EventType: "input_pinned", PayloadHash: testHash, Versions: []VersionIdentity{version("orchestrator")}}
	first, err := AppendAuditChain("", firstRecord)
	if err != nil {
		t.Fatal(err)
	}
	secondRecord := AuditRecord{Ordinal: 2, AttemptID: "attempt-1", EventType: "evidence_pinned", PayloadHash: testHash, Versions: []VersionIdentity{version("retrieval")}}
	second, err := AppendAuditChain(first.ChainHash, secondRecord)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ChainHash.Valid() || !second.ChainHash.Valid() || first.ChainHash == second.ChainHash || second.PreviousHash != first.ChainHash {
		t.Fatalf("invalid audit chain: %#v %#v", first, second)
	}
	changed, err := AppendAuditChain("", secondRecord)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ChainHash == second.ChainHash {
		t.Fatal("audit chain ignored previous hash")
	}
}

func reverseCopy[T any](values []T) []T {
	result := append([]T(nil), values...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}
