package orchestration

import (
	"context"
	"errors"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

const RecoveryMissingProviderReceiptCode = "AI_PROVIDER_RECEIPT_MISSING_AFTER_RESTART"

var (
	ErrAIRecoveryInvalid  = errors.New("AI Job recovery facts are invalid")
	ErrAIRecoveryConflict = errors.New("AI Job recovery decision conflicts with durable state")
)

type DeterministicRecoveryIdentity struct {
	JobID                domain.ID       `json:"job_id"`
	Phase                JobPhase        `json:"phase"`
	InputHash            aicontract.Hash `json:"input_hash"`
	EvidenceManifestHash aicontract.Hash `json:"evidence_manifest_hash"`
	DependencyHash       aicontract.Hash `json:"dependency_hash"`
	CancelGeneration     int64           `json:"cancel_generation"`
}

func (identity DeterministicRecoveryIdentity) Valid() bool {
	return identity.JobID.Valid() && deterministicRecoveryPhase(identity.Phase) && identity.InputHash.Valid() && identity.EvidenceManifestHash.Valid() && identity.DependencyHash.Valid() && identity.CancelGeneration >= 0
}

type DeterministicCheckpoint struct {
	DeterministicRecoveryIdentity
	OutputHash aicontract.Hash `json:"output_hash"`
}

func (checkpoint DeterministicCheckpoint) Valid() bool {
	return checkpoint.DeterministicRecoveryIdentity.Valid() && checkpoint.OutputHash.Valid()
}

func (checkpoint DeterministicCheckpoint) Matches(expected DeterministicRecoveryIdentity) bool {
	return checkpoint.Valid() && expected.Valid() && checkpoint.DeterministicRecoveryIdentity == expected
}

type AIRecoverySnapshot struct {
	State      AIJobState
	Pinned     DeterministicRecoveryIdentity
	Attempt    *AttemptRecord
	Response   *TerminalResponseReceipt
	Outcome    *AttemptOutcomeReceipt
	PatchSeal  *AttemptPatchSeal
	Checkpoint *DeterministicCheckpoint
}

func (snapshot AIRecoverySnapshot) Valid() bool {
	if !snapshot.State.Valid() || !snapshot.Pinned.Valid() || snapshot.Pinned.JobID != snapshot.State.Job.ID || snapshot.Pinned.InputHash != aicontract.Hash(snapshot.State.Job.InputHash) || snapshot.Pinned.CancelGeneration != snapshot.State.Job.CancelGeneration {
		return false
	}
	if snapshot.Attempt == nil {
		return snapshot.Response == nil && snapshot.Outcome == nil && snapshot.PatchSeal == nil && validRecoveryCheckpoint(snapshot.State, snapshot.Checkpoint)
	}
	if !snapshot.Attempt.Valid() || snapshot.Attempt.JobID != snapshot.State.Job.ID || snapshot.Attempt.Manifest.InputHash != snapshot.Pinned.InputHash || snapshot.Attempt.Manifest.EvidenceManifestHash != snapshot.Pinned.EvidenceManifestHash || int64(snapshot.Attempt.Manifest.CancelGeneration) != snapshot.Pinned.CancelGeneration || !validRecoveryCheckpoint(snapshot.State, snapshot.Checkpoint) {
		return false
	}
	attemptID := snapshot.Attempt.AttemptID
	if snapshot.Response != nil && (!snapshot.Response.Valid() || snapshot.Response.JobID != snapshot.State.Job.ID || snapshot.Response.AttemptID != attemptID) {
		return false
	}
	if snapshot.Outcome != nil && (!snapshot.Outcome.Valid() || snapshot.Outcome.JobID != snapshot.State.Job.ID || snapshot.Outcome.AttemptID != attemptID) {
		return false
	}
	if snapshot.PatchSeal != nil && (!snapshot.PatchSeal.Valid() || snapshot.PatchSeal.JobID != snapshot.State.Job.ID || snapshot.PatchSeal.AttemptID != attemptID) {
		return false
	}
	if snapshot.Outcome != nil && snapshot.Outcome.Outcome == aicontract.OutcomeSucceeded && snapshot.Response == nil {
		return false
	}
	return true
}

func (snapshot AIRecoverySnapshot) hasProviderTerminalReceipt() bool {
	return snapshot.Response != nil || snapshot.Outcome != nil
}

type AIRecoveryInterruption struct {
	ExpectedState AIJobState
	Outcome       *AttemptOutcomeReceipt
	Event         AIJobEventDraft
}

func (interruption AIRecoveryInterruption) Valid() bool {
	state := interruption.ExpectedState
	if !state.Valid() || state.Job.Status != sharedjob.Running || state.Owner == "" || interruption.Event.JobID != state.Job.ID || interruption.Event.Kind != EventTerminal || interruption.Event.Phase != state.Phase || interruption.Event.Outcome != aicontract.OutcomeInterrupted || interruption.Event.SafeErrorCode != RecoveryMissingProviderReceiptCode || !interruption.Event.Valid() {
		return false
	}
	if interruption.Outcome == nil {
		return interruption.Event.AttemptID == ""
	}
	return interruption.Outcome.Valid() && interruption.Outcome.JobID == state.Job.ID && interruption.Outcome.Outcome == aicontract.OutcomeInterrupted && interruption.Outcome.ErrorCode == RecoveryMissingProviderReceiptCode && interruption.Event.AttemptID == interruption.Outcome.AttemptID
}

type AIRecoveryRepository interface {
	LoadAIRecoverySnapshot(context.Context, domain.ID) (AIRecoverySnapshot, error)
	InterruptAIJobRecovery(context.Context, AIRecoveryInterruption) (AIJobState, bool, error)
}

type DeterministicRecoveryExecutor interface {
	ReuseDeterministicCheckpoint(context.Context, DeterministicCheckpoint) error
	RerunDeterministicPhase(context.Context, DeterministicRecoveryIdentity) error
}

type AIRecoveryAction string

const (
	RecoveryReusedCheckpoint    AIRecoveryAction = "reused_checkpoint"
	RecoveryReranDeterministic  AIRecoveryAction = "reran_deterministic"
	RecoveryInterruptedProvider AIRecoveryAction = "interrupted_provider"
	RecoveryObservedTerminal    AIRecoveryAction = "observed_terminal_receipt"
	RecoveryAlreadyInterrupted  AIRecoveryAction = "already_interrupted"
)

type AIRecoveryResult struct {
	JobID  domain.ID
	Action AIRecoveryAction
	State  AIJobState
}

type AIRecoveryService struct {
	Repository AIRecoveryRepository
	Executor   DeterministicRecoveryExecutor
}

// Recover has deliberately no Provider port. A restart may reuse or rerun a
// deterministic step, but it can never make a model invocation.
func (service AIRecoveryService) Recover(ctx context.Context, expected DeterministicRecoveryIdentity) (AIRecoveryResult, error) {
	if ctx == nil || service.Repository == nil || service.Executor == nil || !expected.Valid() {
		return AIRecoveryResult{}, ErrAIRecoveryInvalid
	}
	snapshot, err := service.Repository.LoadAIRecoverySnapshot(ctx, expected.JobID)
	if err != nil {
		return AIRecoveryResult{}, err
	}
	if !snapshot.Valid() || snapshot.State.Job.ID != expected.JobID || snapshot.Pinned != expected {
		return AIRecoveryResult{}, ErrAIRecoveryInvalid
	}
	if snapshot.State.Job.Status == sharedjob.Interrupted {
		return AIRecoveryResult{JobID: expected.JobID, Action: RecoveryAlreadyInterrupted, State: snapshot.State}, nil
	}
	if snapshot.State.Job.Status != sharedjob.Running {
		return AIRecoveryResult{}, ErrAIRecoveryConflict
	}

	providerWasOrMayHaveBeenInvoked := snapshot.Attempt != nil || snapshot.State.Phase.Order() >= PhaseProviderToolLoop.Order()
	if providerWasOrMayHaveBeenInvoked && !snapshot.hasProviderTerminalReceipt() {
		return service.interrupt(ctx, snapshot)
	}
	if snapshot.Outcome != nil && snapshot.Outcome.Outcome != aicontract.OutcomeSucceeded {
		return AIRecoveryResult{JobID: expected.JobID, Action: RecoveryObservedTerminal, State: snapshot.State}, nil
	}
	if snapshot.Response != nil && expected.Phase.Order() < PhaseDeterministicPreview.Order() {
		return AIRecoveryResult{}, ErrAIRecoveryInvalid
	}
	if snapshot.Attempt == nil && snapshot.State.Phase.Order() >= PhaseProviderToolLoop.Order() {
		return AIRecoveryResult{}, ErrAIRecoveryConflict
	}

	if snapshot.Checkpoint != nil && snapshot.Checkpoint.Matches(expected) {
		if err := service.Executor.ReuseDeterministicCheckpoint(ctx, *snapshot.Checkpoint); err != nil {
			return AIRecoveryResult{}, err
		}
		return AIRecoveryResult{JobID: expected.JobID, Action: RecoveryReusedCheckpoint, State: snapshot.State}, nil
	}
	if err := service.Executor.RerunDeterministicPhase(ctx, expected); err != nil {
		return AIRecoveryResult{}, err
	}
	return AIRecoveryResult{JobID: expected.JobID, Action: RecoveryReranDeterministic, State: snapshot.State}, nil
}

func (service AIRecoveryService) interrupt(ctx context.Context, snapshot AIRecoverySnapshot) (AIRecoveryResult, error) {
	event := AIJobEventDraft{
		JobID: snapshot.State.Job.ID, EventKey: "recovery-provider-receipt-missing", Kind: EventTerminal,
		Phase: snapshot.State.Phase, Progress: snapshot.State.Phase.Progress(), Outcome: aicontract.OutcomeInterrupted, SafeErrorCode: RecoveryMissingProviderReceiptCode,
	}
	var outcome *AttemptOutcomeReceipt
	if snapshot.Attempt != nil {
		receipt, err := NewAttemptOutcomeReceipt(snapshot.State.Job.ID, snapshot.Attempt.AttemptID, aicontract.OutcomeInterrupted, RecoveryMissingProviderReceiptCode)
		if err != nil {
			return AIRecoveryResult{}, ErrAIRecoveryInvalid
		}
		outcome = &receipt
		event.AttemptID = snapshot.Attempt.AttemptID
	}
	interruption := AIRecoveryInterruption{ExpectedState: snapshot.State, Outcome: outcome, Event: event}
	if !interruption.Valid() {
		return AIRecoveryResult{}, ErrAIRecoveryInvalid
	}
	state, _, err := service.Repository.InterruptAIJobRecovery(ctx, interruption)
	if err != nil {
		return AIRecoveryResult{}, err
	}
	if !state.Valid() || state.Job.ID != snapshot.State.Job.ID || state.Job.Status != sharedjob.Interrupted || state.Phase != snapshot.State.Phase || state.Owner != "" || state.Job.CancelGeneration != snapshot.State.Job.CancelGeneration {
		return AIRecoveryResult{}, ErrAIRecoveryConflict
	}
	return AIRecoveryResult{JobID: state.Job.ID, Action: RecoveryInterruptedProvider, State: state}, nil
}

func deterministicRecoveryPhase(phase JobPhase) bool {
	return phase == PhaseInputPinned || phase == PhaseEvidencePinned || phase == PhaseDeterministicPreview || phase == PhasePatchSealed
}

func validRecoveryCheckpoint(state AIJobState, checkpoint *DeterministicCheckpoint) bool {
	return checkpoint == nil || checkpoint.Valid() && checkpoint.JobID == state.Job.ID && checkpoint.CancelGeneration == state.Job.CancelGeneration && checkpoint.Phase.Order() <= state.Phase.Order()
}
