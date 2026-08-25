package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
)

type attemptLedgerRepositoryFake struct {
	attempts  map[aicontract.AttemptID]AttemptRecord
	ordinals  map[domain.ID]int
	responses map[aicontract.AttemptID]TerminalResponseReceipt
	outcomes  map[aicontract.AttemptID]AttemptOutcomeReceipt
	patches   map[aicontract.AttemptID]AttemptPatchSeal
}

func newAttemptLedgerRepositoryFake() *attemptLedgerRepositoryFake {
	return &attemptLedgerRepositoryFake{attempts: map[aicontract.AttemptID]AttemptRecord{}, ordinals: map[domain.ID]int{}, responses: map[aicontract.AttemptID]TerminalResponseReceipt{}, outcomes: map[aicontract.AttemptID]AttemptOutcomeReceipt{}, patches: map[aicontract.AttemptID]AttemptPatchSeal{}}
}

func (repository *attemptLedgerRepositoryFake) InsertAttempt(_ context.Context, record AttemptRecord) (AttemptRecord, bool, error) {
	if existing, found := repository.attempts[record.AttemptID]; found {
		if !equalAttemptRecord(existing, record) {
			return AttemptRecord{}, false, ErrAttemptLedgerConflict
		}
		return cloneAttemptRecord(existing), true, nil
	}
	if record.Kind == AttemptInitial && repository.ordinals[record.JobID] != 0 || record.Kind == AttemptRepair && (repository.ordinals[record.JobID]+1 != record.Ordinal || repository.attempts[record.ParentID].JobID != record.JobID) || record.Kind == AttemptRetry && repository.attempts[record.ParentID].JobID != record.ParentJobID {
		return AttemptRecord{}, false, ErrAttemptLedgerConflict
	}
	repository.attempts[record.AttemptID] = cloneAttemptRecord(record)
	repository.ordinals[record.JobID] = record.Ordinal
	return cloneAttemptRecord(record), false, nil
}

func (repository *attemptLedgerRepositoryFake) InsertTerminalResponse(_ context.Context, receipt TerminalResponseReceipt) (TerminalResponseReceipt, bool, error) {
	if _, found := repository.attempts[receipt.AttemptID]; !found {
		return TerminalResponseReceipt{}, false, ErrAttemptLedgerConflict
	}
	if existing, found := repository.responses[receipt.AttemptID]; found {
		if !equalResponseReceipt(existing, receipt) {
			return TerminalResponseReceipt{}, false, ErrAttemptLedgerConflict
		}
		return cloneResponseReceipt(existing), true, nil
	}
	repository.responses[receipt.AttemptID] = cloneResponseReceipt(receipt)
	return cloneResponseReceipt(receipt), false, nil
}

func (repository *attemptLedgerRepositoryFake) InsertAttemptOutcome(_ context.Context, receipt AttemptOutcomeReceipt) (AttemptOutcomeReceipt, bool, error) {
	if _, found := repository.attempts[receipt.AttemptID]; !found {
		return AttemptOutcomeReceipt{}, false, ErrAttemptLedgerConflict
	}
	if existing, found := repository.outcomes[receipt.AttemptID]; found {
		if existing != receipt {
			return AttemptOutcomeReceipt{}, false, ErrAttemptLedgerConflict
		}
		return existing, true, nil
	}
	repository.outcomes[receipt.AttemptID] = receipt
	return receipt, false, nil
}

func (repository *attemptLedgerRepositoryFake) InsertAttemptPatchSeal(_ context.Context, seal AttemptPatchSeal) (AttemptPatchSeal, bool, error) {
	if _, found := repository.attempts[seal.AttemptID]; !found {
		return AttemptPatchSeal{}, false, ErrAttemptLedgerConflict
	}
	if existing, found := repository.patches[seal.AttemptID]; found {
		if existing != seal {
			return AttemptPatchSeal{}, false, ErrAttemptLedgerConflict
		}
		return existing, true, nil
	}
	repository.patches[seal.AttemptID] = seal
	return seal, false, nil
}

func TestAttemptLedgerRetainsInitialRepairAndExplicitRetryLineage(t *testing.T) {
	repository := newAttemptLedgerRepositoryFake()
	ledger := AttemptLedger{Repository: repository}
	job1, _ := domain.NewID()
	job2, _ := domain.NewID()
	attempt1 := aicontract.AttemptID("018f9e40-0000-7000-8000-000000000210")
	attempt2 := aicontract.AttemptID("018f9e40-0000-7000-8000-000000000211")
	attempt3 := aicontract.AttemptID("018f9e40-0000-7000-8000-000000000212")
	initial, err := NewAttemptRecord(job1, AttemptInitial, 1, 0, "", "", attemptLedgerManifest(attempt1))
	if err != nil {
		t.Fatal(err)
	}
	repair, err := NewAttemptRecord(job1, AttemptRepair, 2, 1, "", attempt1, attemptLedgerManifest(attempt2))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := NewAttemptRecord(job2, AttemptRetry, 1, 0, job1, attempt2, attemptLedgerManifest(attempt3))
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []AttemptRecord{initial, repair, retry} {
		stored, replay, createErr := ledger.Create(context.Background(), record)
		if createErr != nil || replay || !stored.Valid() || stored.ManifestHash != record.ManifestHash {
			t.Fatalf("stored=%#v replay=%v err=%v", stored, replay, createErr)
		}
	}
	if len(repository.attempts) != 3 || repository.ordinals[job1] != 2 || repository.ordinals[job2] != 1 {
		t.Fatalf("attempts=%#v ordinals=%#v", repository.attempts, repository.ordinals)
	}
	if _, replay, err := ledger.Create(context.Background(), initial); err != nil || !replay {
		t.Fatalf("initial replay=%v err=%v", replay, err)
	}
	tampered := initial
	tampered.Manifest.Tools = append([]aicontract.VersionIdentity(nil), initial.Manifest.Tools...)
	tampered.Manifest.Tools[0].Hash = aicontract.Hash(strings.Repeat("f", 64))
	if _, _, err := ledger.Create(context.Background(), tampered); !errors.Is(err, ErrAttemptLedgerInvalid) {
		t.Fatalf("tampered manifest err=%v", err)
	}
}

func TestAttemptLedgerEnforcesOneTerminalResponseOutcomeAndPatchSeal(t *testing.T) {
	repository := newAttemptLedgerRepositoryFake()
	ledger := AttemptLedger{Repository: repository}
	jobID, _ := domain.NewID()
	attemptID := aicontract.AttemptID("018f9e40-0000-7000-8000-000000000220")
	record, _ := NewAttemptRecord(jobID, AttemptInitial, 1, 0, "", "", attemptLedgerManifest(attemptID))
	_, _, _ = ledger.Create(context.Background(), record)

	response, err := NewTerminalResponseReceipt(jobID, attemptID, attemptLedgerResponse())
	if err != nil {
		t.Fatal(err)
	}
	if _, replay, err := ledger.RecordResponse(context.Background(), response); err != nil || replay {
		t.Fatalf("response replay=%v err=%v", replay, err)
	}
	if _, replay, err := ledger.RecordResponse(context.Background(), response); err != nil || !replay {
		t.Fatalf("response replay=%v err=%v", replay, err)
	}
	conflictingResponse := response
	conflictingResponse.Response.OriginalBodyHash = aicontract.Hash(strings.Repeat("f", 64))
	conflictingResponse, _ = NewTerminalResponseReceipt(jobID, attemptID, conflictingResponse.Response)
	if _, _, err := ledger.RecordResponse(context.Background(), conflictingResponse); !errors.Is(err, ErrAttemptLedgerConflict) {
		t.Fatalf("second response err=%v", err)
	}

	outcome, _ := NewAttemptOutcomeReceipt(jobID, attemptID, aicontract.OutcomeSucceeded, "")
	if _, replay, err := ledger.Complete(context.Background(), outcome); err != nil || replay {
		t.Fatalf("outcome replay=%v err=%v", replay, err)
	}
	failed, _ := NewAttemptOutcomeReceipt(jobID, attemptID, aicontract.OutcomeFailed, "PROVIDER_FAILED")
	if _, _, err := ledger.Complete(context.Background(), failed); !errors.Is(err, ErrAttemptLedgerConflict) {
		t.Fatalf("second outcome err=%v", err)
	}

	patch1, _ := NewAttemptPatchSeal(jobID, attemptID, "018f9e40-0000-7000-8000-000000000230", aicontract.Hash(strings.Repeat("a", 64)))
	if _, replay, err := ledger.SealPatch(context.Background(), patch1); err != nil || replay {
		t.Fatalf("patch replay=%v err=%v", replay, err)
	}
	if _, replay, err := ledger.SealPatch(context.Background(), patch1); err != nil || !replay {
		t.Fatalf("patch replay=%v err=%v", replay, err)
	}
	patch2, _ := NewAttemptPatchSeal(jobID, attemptID, "018f9e40-0000-7000-8000-000000000231", aicontract.Hash(strings.Repeat("b", 64)))
	if _, _, err := ledger.SealPatch(context.Background(), patch2); !errors.Is(err, ErrAttemptLedgerConflict) {
		t.Fatalf("second patch err=%v", err)
	}
}

func TestAttemptOutcomeRejectsRunningLateAndMissingStableFailureCode(t *testing.T) {
	jobID, _ := domain.NewID()
	attemptID := aicontract.AttemptID("018f9e40-0000-7000-8000-000000000240")
	for _, outcome := range []aicontract.AttemptOutcome{aicontract.OutcomeRunning, aicontract.OutcomeIgnoredLateResult} {
		if _, err := NewAttemptOutcomeReceipt(jobID, attemptID, outcome, "CODE"); !errors.Is(err, ErrAttemptLedgerInvalid) {
			t.Fatalf("outcome=%s err=%v", outcome, err)
		}
	}
	if _, err := NewAttemptOutcomeReceipt(jobID, attemptID, aicontract.OutcomeFailed, ""); !errors.Is(err, ErrAttemptLedgerInvalid) {
		t.Fatalf("missing failure code err=%v", err)
	}
}

func attemptLedgerManifest(attemptID aicontract.AttemptID) aiprovider.AttemptManifest {
	hash := aicontract.Hash(strings.Repeat("a", 64))
	fixture := aicontract.V1Fixture()
	tools := make([]aicontract.VersionIdentity, len(fixture.Tools))
	for index, tool := range fixture.Tools {
		tools[index] = tool.Identity
	}
	return aiprovider.AttemptManifest{AttemptID: attemptID, Provider: aicontract.VersionIdentity{ID: "provider", Version: "v1", Hash: hash}, Model: aicontract.VersionIdentity{ID: "model", Version: "v1", Hash: hash}, EndpointClassification: aiprovider.EndpointLoopback, Prompt: fixture.Prompt.Identity, StructuredResponseSchema: fixture.PatchSchema.Identity, Tools: tools, Orchestrator: fixture.Orchestrator.Identity, Budget: fixture.Budget.Identity, InputHash: hash, EvidenceManifestHash: hash}
}

func attemptLedgerResponse() aiaudit.SealedProviderResponse {
	body := []byte(`{"ok":true}`)
	hash := sha256.Sum256(body)
	return aiaudit.SealedProviderResponse{Schema: aicontract.V1Fixture().PatchSchema.Identity, OriginalBodyHash: aicontract.Hash(strings.Repeat("b", 64)), StoredBody: body, StoredBodyHash: aicontract.Hash(hex.EncodeToString(hash[:]))}
}
