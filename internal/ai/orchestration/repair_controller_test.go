package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipatch "github.com/zouyi/eco-guardian/internal/ai/patch"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

type repairStoreFake struct {
	transitions []RepairTransition
	failOnce    bool
}

func (s *repairStoreFake) CommitRepairTransition(_ context.Context, value RepairTransition) error {
	if s.failOnce {
		s.failOnce = false
		return errors.New("store unavailable")
	}
	copy := RepairTransition{Terminal: value.Terminal, Next: cloneRepairRecord(value.Next)}
	copy.Terminal.Manifest = cloneRepairManifest(value.Terminal.Manifest)
	s.transitions = append(s.transitions, copy)
	return nil
}

func TestRepairControllerCreatesExactlyThreeOrderedFrozenAttempts(t *testing.T) {
	input := candidateScope(t).Input
	initial := repairManifest(t, input, "attempt-1")
	store := &repairStoreFake{}
	controller, err := NewRepairController(input, initial, store)
	if err != nil {
		t.Fatal(err)
	}
	response := repairResponse(initial.StructuredResponseSchema)
	for round := 1; round <= 3; round++ {
		nextID := aicontract.AttemptID(fmt.Sprintf("attempt-%d", round+1))
		plan, err := controller.HandleFailure(context.Background(), &aipatch.DecodeError{Code: aipatch.ErrorSchemaViolation}, response, nextID)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Manifest.AttemptID != nextID || plan.Instruction.Round != round || plan.Instruction.ParentAttemptID != aicontract.AttemptID(fmt.Sprintf("attempt-%d", round)) ||
			plan.Instruction.Diagnostic.Code != string(aipatch.ErrorSchemaViolation) || !sameFrozenRepairManifest(initial, plan.Manifest) {
			t.Fatalf("round=%d plan=%#v", round, plan)
		}
		for _, forbidden := range []string{"goals", "constraints", "allowed_targets", "evidence_manifest_hash", "tools", "super-secret"} {
			if strings.Contains(string(plan.Canonical), forbidden) {
				t.Fatalf("repair instruction leaked frozen context %q: %s", forbidden, plan.Canonical)
			}
		}
		plan.Manifest.Tools[0].ID = "mutated-by-caller"
		plan.Instruction.RedactedResponse[0] = '['
	}
	if len(store.transitions) != 3 {
		t.Fatalf("transitions=%d", len(store.transitions))
	}
	for index, transition := range store.transitions {
		ordinal := index + 1
		wantParent := aicontract.AttemptID("")
		if ordinal > 1 {
			wantParent = aicontract.AttemptID(fmt.Sprintf("attempt-%d", ordinal-1))
		}
		if transition.Terminal.Ordinal != ordinal || transition.Terminal.AttemptID != aicontract.AttemptID(fmt.Sprintf("attempt-%d", ordinal)) ||
			transition.Terminal.ParentAttemptID != wantParent ||
			transition.Terminal.Outcome != aicontract.OutcomeFailed || transition.Next == nil || transition.Next.Ordinal != ordinal+1 ||
			transition.Next.ParentAttemptID != transition.Terminal.AttemptID || transition.Next.Outcome != aicontract.OutcomeRunning ||
			!sameFrozenRepairManifest(initial, transition.Terminal.Manifest) || !sameFrozenRepairManifest(initial, transition.Next.Manifest) {
			t.Fatalf("transition[%d]=%#v", index, transition)
		}
	}
	if _, err = controller.HandleFailure(context.Background(), &aipatch.DecodeError{Code: aipatch.ErrorSchemaViolation}, response, "attempt-5"); !errors.Is(err, ErrRepairExhausted) {
		t.Fatalf("fourth repair err=%v", err)
	}
	if len(store.transitions) != 4 || store.transitions[3].Next != nil || store.transitions[3].Terminal.AttemptID != "attempt-4" {
		t.Fatalf("exhausted transition=%#v", store.transitions)
	}
	if _, err = controller.HandleFailure(context.Background(), &aipatch.DecodeError{Code: aipatch.ErrorSchemaViolation}, response, "attempt-6"); !errors.Is(err, ErrRepairClosed) {
		t.Fatalf("closed err=%v", err)
	}
}

func TestRepairControllerTerminatesPolicyEvidenceAndSemanticFailures(t *testing.T) {
	input := candidateScope(t).Input
	tests := []error{
		&aipatch.DecodeError{Code: aipatch.ErrorEvidenceViolation},
		&aipatch.DecodeError{Code: aipatch.ErrorScopeViolation},
		&DeterministicBlockError{Code: "FULL_VALIDATION_BLOCK"},
	}
	for _, failure := range tests {
		t.Run(failure.Error(), func(t *testing.T) {
			initial := repairManifest(t, input, "attempt-1")
			store := &repairStoreFake{}
			controller, err := NewRepairController(input, initial, store)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = controller.HandleFailure(context.Background(), failure, repairResponse(initial.StructuredResponseSchema), "attempt-2"); !errors.Is(err, ErrRepairNotAllowed) {
				t.Fatalf("err=%v", err)
			}
			if len(store.transitions) != 1 || store.transitions[0].Next != nil || store.transitions[0].Terminal.Outcome != aicontract.OutcomeFailed {
				t.Fatalf("transitions=%#v", store.transitions)
			}
		})
	}
}

func TestRepairControllerDoesNotAdvanceOnPersistenceOrResponseFailure(t *testing.T) {
	input := candidateScope(t).Input
	initial := repairManifest(t, input, "attempt-1")
	store := &repairStoreFake{failOnce: true}
	controller, err := NewRepairController(input, initial, store)
	if err != nil {
		t.Fatal(err)
	}
	response := repairResponse(initial.StructuredResponseSchema)
	failure := &aipatch.DecodeError{Code: aipatch.ErrorMalformedJSON}
	if _, err = controller.HandleFailure(context.Background(), failure, response, "attempt-2"); !errors.Is(err, ErrRepairPersistence) {
		t.Fatalf("persistence err=%v", err)
	}
	plan, err := controller.HandleFailure(context.Background(), failure, response, "attempt-2")
	if err != nil || plan.Instruction.Round != 1 || len(store.transitions) != 1 {
		t.Fatalf("retry plan=%#v err=%v transitions=%#v", plan, err, store.transitions)
	}

	invalidStore := &repairStoreFake{}
	invalidController, _ := NewRepairController(input, initial, invalidStore)
	invalid := repairResponse(initial.StructuredResponseSchema)
	invalid.StoredBodyHash = aicontract.Hash(strings.Repeat("f", 64))
	if _, err = invalidController.HandleFailure(context.Background(), failure, invalid, "attempt-2"); !errors.Is(err, ErrRepairControllerInvalid) || len(invalidStore.transitions) != 0 {
		t.Fatalf("invalid response err=%v transitions=%#v", err, invalidStore.transitions)
	}
}

func repairManifest(t *testing.T, input aicontract.AIDesignInputV1, attemptID aicontract.AttemptID) aiprovider.AttemptManifest {
	t.Helper()
	inputHash, err := aicontract.HashAIDesignInputV1(input)
	if err != nil {
		t.Fatal(err)
	}
	fixture := aicontract.V1Fixture()
	tools := make([]aicontract.VersionIdentity, len(fixture.Tools))
	for index, tool := range fixture.Tools {
		tools[index] = tool.Identity
	}
	manifest := aiprovider.AttemptManifest{
		AttemptID: attemptID, Provider: repairIdentity("provider"), Model: repairIdentity("model"),
		EndpointClassification: aiprovider.EndpointLoopback, Prompt: fixture.Prompt.Identity,
		StructuredResponseSchema: fixture.PatchSchema.Identity, Tools: tools, Orchestrator: fixture.Orchestrator.Identity,
		Budget: fixture.Budget.Identity, InputHash: inputHash, EvidenceManifestHash: aicontract.Hash(strings.Repeat("b", 64)), CancelGeneration: 1,
	}
	if !manifest.Valid() {
		t.Fatalf("manifest=%#v", manifest)
	}
	return manifest
}

func repairIdentity(id string) aicontract.VersionIdentity {
	return aicontract.VersionIdentity{ID: id, Version: "v1", Hash: aicontract.Hash(strings.Repeat("a", 64))}
}

func repairResponse(schema aicontract.VersionIdentity) aiaudit.SealedProviderResponse {
	body := json.RawMessage("{\"diagnostic\":\"[REDACTED]\"}")
	sum := sha256.Sum256(body)
	return aiaudit.SealedProviderResponse{
		Schema: schema, OriginalBodyHash: aicontract.Hash(strings.Repeat("c", 64)),
		StoredBody: body, StoredBodyHash: aicontract.Hash(hex.EncodeToString(sum[:])),
	}
}
