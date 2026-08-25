package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type aiRecoveryRepositoryFake struct {
	snapshot     AIRecoverySnapshot
	interruption *AIRecoveryInterruption
	commits      int
}

func (repository *aiRecoveryRepositoryFake) LoadAIRecoverySnapshot(_ context.Context, jobID domain.ID) (AIRecoverySnapshot, error) {
	if repository.snapshot.State.Job.ID != jobID {
		return AIRecoverySnapshot{}, ErrAIRecoveryInvalid
	}
	return repository.snapshot, nil
}

func (repository *aiRecoveryRepositoryFake) InterruptAIJobRecovery(_ context.Context, interruption AIRecoveryInterruption) (AIJobState, bool, error) {
	if !interruption.Valid() || repository.snapshot.State.Job.ID != interruption.ExpectedState.Job.ID {
		return AIJobState{}, false, ErrAIRecoveryInvalid
	}
	if repository.interruption != nil {
		return repository.snapshot.State, true, nil
	}
	if repository.snapshot.State.Job.Status != interruption.ExpectedState.Job.Status || repository.snapshot.State.Phase != interruption.ExpectedState.Phase || repository.snapshot.State.Owner != interruption.ExpectedState.Owner || repository.snapshot.State.Job.CancelGeneration != interruption.ExpectedState.Job.CancelGeneration {
		return AIJobState{}, false, ErrAIRecoveryConflict
	}
	repository.commits++
	copy := interruption
	repository.interruption = &copy
	repository.snapshot.Outcome = interruption.Outcome
	repository.snapshot.State.Job.Status = sharedjob.Interrupted
	repository.snapshot.State.Owner = ""
	return repository.snapshot.State, false, nil
}

type deterministicRecoveryExecutorFake struct {
	reused []DeterministicCheckpoint
	rerun  []DeterministicRecoveryIdentity
	err    error
}

func (executor *deterministicRecoveryExecutorFake) ReuseDeterministicCheckpoint(_ context.Context, checkpoint DeterministicCheckpoint) error {
	executor.reused = append(executor.reused, checkpoint)
	return executor.err
}

func (executor *deterministicRecoveryExecutorFake) RerunDeterministicPhase(_ context.Context, identity DeterministicRecoveryIdentity) error {
	executor.rerun = append(executor.rerun, identity)
	return executor.err
}

func recoveryState(t *testing.T, phase JobPhase) AIJobState {
	t.Helper()
	_, controller, state := newCancellationJob(t, "recovery-"+string(phase))
	state, _, _ = controller.Claim(context.Background(), state, "worker-before-restart")
	for _, next := range []JobPhase{PhaseEvidencePinned, PhaseProviderToolLoop, PhaseDeterministicPreview, PhasePatchSealed} {
		if state.Phase == phase {
			break
		}
		state, _, _ = controller.Advance(context.Background(), state, next)
	}
	return state
}

func recoveryIdentity(state AIJobState, phase JobPhase) DeterministicRecoveryIdentity {
	return DeterministicRecoveryIdentity{
		JobID: state.Job.ID, Phase: phase, InputHash: aicontract.Hash(state.Job.InputHash),
		EvidenceManifestHash: aicontract.Hash(strings.Repeat("b", 64)), DependencyHash: aicontract.Hash(strings.Repeat("c", 64)), CancelGeneration: state.Job.CancelGeneration,
	}
}

func recoveryAttempt(t *testing.T, state AIJobState, expected DeterministicRecoveryIdentity) AttemptRecord {
	t.Helper()
	manifest := attemptLedgerManifest("018f9e40-0000-7000-8000-000000000260")
	manifest.InputHash = expected.InputHash
	manifest.EvidenceManifestHash = expected.EvidenceManifestHash
	manifest.CancelGeneration = uint64(expected.CancelGeneration)
	record, err := NewAttemptRecord(state.Job.ID, AttemptInitial, 1, 0, "", "", manifest)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestRecoveryReusesOnlyMatchingDeterministicCheckpoint(t *testing.T) {
	state := recoveryState(t, PhaseEvidencePinned)
	expected := recoveryIdentity(state, PhaseEvidencePinned)
	checkpoint := DeterministicCheckpoint{DeterministicRecoveryIdentity: expected, OutputHash: aicontract.Hash(strings.Repeat("d", 64))}
	repository := &aiRecoveryRepositoryFake{snapshot: AIRecoverySnapshot{State: state, Pinned: expected, Checkpoint: &checkpoint}}
	executor := &deterministicRecoveryExecutorFake{}
	result, err := (AIRecoveryService{Repository: repository, Executor: executor}).Recover(context.Background(), expected)
	if err != nil || result.Action != RecoveryReusedCheckpoint || len(executor.reused) != 1 || len(executor.rerun) != 0 || repository.commits != 0 {
		t.Fatalf("result=%#v reused=%d rerun=%d commits=%d err=%v", result, len(executor.reused), len(executor.rerun), repository.commits, err)
	}
}

func TestRecoveryRerunsPinnedDeterministicIdentityWhenCheckpointMissingOrMismatched(t *testing.T) {
	state := recoveryState(t, PhaseEvidencePinned)
	expected := recoveryIdentity(state, PhaseEvidencePinned)
	mismatched := expected
	mismatched.DependencyHash = aicontract.Hash(strings.Repeat("e", 64))
	checkpoint := DeterministicCheckpoint{DeterministicRecoveryIdentity: mismatched, OutputHash: aicontract.Hash(strings.Repeat("d", 64))}
	repository := &aiRecoveryRepositoryFake{snapshot: AIRecoverySnapshot{State: state, Pinned: expected, Checkpoint: &checkpoint}}
	executor := &deterministicRecoveryExecutorFake{}
	result, err := (AIRecoveryService{Repository: repository, Executor: executor}).Recover(context.Background(), expected)
	if err != nil || result.Action != RecoveryReranDeterministic || len(executor.reused) != 0 || len(executor.rerun) != 1 || executor.rerun[0] != expected {
		t.Fatalf("result=%#v reused=%d rerun=%#v err=%v", result, len(executor.reused), executor.rerun, err)
	}
	changedExpectation := expected
	changedExpectation.DependencyHash = aicontract.Hash(strings.Repeat("f", 64))
	if _, err := (AIRecoveryService{Repository: repository, Executor: executor}).Recover(context.Background(), changedExpectation); !errors.Is(err, ErrAIRecoveryInvalid) {
		t.Fatalf("changed pinned identity err=%v", err)
	}
}

func TestRecoveryInterruptsProviderAttemptWithoutReceiptAndNeverExecutesWork(t *testing.T) {
	state := recoveryState(t, PhaseProviderToolLoop)
	expected := recoveryIdentity(state, PhaseDeterministicPreview)
	attempt := recoveryAttempt(t, state, expected)
	repository := &aiRecoveryRepositoryFake{snapshot: AIRecoverySnapshot{State: state, Pinned: expected, Attempt: &attempt}}
	executor := &deterministicRecoveryExecutorFake{}
	service := AIRecoveryService{Repository: repository, Executor: executor}
	result, err := service.Recover(context.Background(), expected)
	if err != nil || result.Action != RecoveryInterruptedProvider || result.State.Job.Status != sharedjob.Interrupted || repository.commits != 1 || len(executor.reused) != 0 || len(executor.rerun) != 0 {
		t.Fatalf("result=%#v commits=%d reused=%d rerun=%d err=%v", result, repository.commits, len(executor.reused), len(executor.rerun), err)
	}
	interruption := repository.interruption
	if interruption == nil || interruption.Outcome == nil || interruption.Outcome.Outcome != aicontract.OutcomeInterrupted || interruption.Outcome.ErrorCode != RecoveryMissingProviderReceiptCode || interruption.Event.Kind != EventTerminal || interruption.Event.SafeErrorCode != RecoveryMissingProviderReceiptCode || interruption.Event.Result != nil {
		t.Fatalf("interruption=%#v", interruption)
	}
	replayed, err := service.Recover(context.Background(), expected)
	if err != nil || replayed.Action != RecoveryAlreadyInterrupted || repository.commits != 1 || len(executor.rerun) != 0 {
		t.Fatalf("replayed=%#v commits=%d err=%v", replayed, repository.commits, err)
	}
}

func TestRecoveryResumesDeterministicWorkOnlyFromDurableProviderResponse(t *testing.T) {
	state := recoveryState(t, PhaseProviderToolLoop)
	expected := recoveryIdentity(state, PhaseDeterministicPreview)
	attempt := recoveryAttempt(t, state, expected)
	response, err := NewTerminalResponseReceipt(state.Job.ID, attempt.AttemptID, attemptLedgerResponse())
	if err != nil {
		t.Fatal(err)
	}
	repository := &aiRecoveryRepositoryFake{snapshot: AIRecoverySnapshot{State: state, Pinned: expected, Attempt: &attempt, Response: &response}}
	executor := &deterministicRecoveryExecutorFake{}
	result, err := (AIRecoveryService{Repository: repository, Executor: executor}).Recover(context.Background(), expected)
	if err != nil || result.Action != RecoveryReranDeterministic || len(executor.rerun) != 1 || repository.commits != 0 {
		t.Fatalf("result=%#v rerun=%d commits=%d err=%v", result, len(executor.rerun), repository.commits, err)
	}
}

var _ AIRecoveryRepository = (*aiRecoveryRepositoryFake)(nil)
var _ DeterministicRecoveryExecutor = (*deterministicRecoveryExecutorFake)(nil)
