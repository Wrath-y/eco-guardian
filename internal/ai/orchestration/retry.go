package orchestration

import (
	"context"
	"errors"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var (
	ErrExplicitRetryInvalid      = errors.New("explicit AI retry request is invalid")
	ErrExplicitRetryNotRetryable = errors.New("AI attempt is not explicitly retryable")
	ErrExplicitRetryStale        = errors.New("AI retry identities are stale")
	ErrExplicitRetryCapability   = errors.New("AI retry capability is unavailable")
	ErrExplicitRetryConflict     = errors.New("explicit AI retry conflicts with durable state")
)

type ExplicitRetryRequest struct {
	ParentJobID    domain.ID
	IdempotencyKey string
	UserConfirmed  bool
}

type ExplicitRetryBasis struct {
	ParentJob              sharedjob.Record
	Input                  aicontract.AIDesignInputV1
	LastAttempt            AttemptRecord
	Outcome                AttemptOutcomeReceipt
	Failure                aiprovider.TerminalError
	NonIdempotentToolCalls int
}

func (basis ExplicitRetryBasis) Valid() bool {
	inputHash, err := aicontract.HashAIDesignInputV1(basis.Input)
	if err != nil || !basis.ParentJob.Valid() || basis.ParentJob.Kind != AIJobKind || (basis.ParentJob.Status != sharedjob.Failed && basis.ParentJob.Status != sharedjob.Interrupted) || basis.ParentJob.ID != basis.LastAttempt.JobID || basis.ParentJob.InputHash != string(inputHash) || domain.ID(basis.Input.Base.ProjectID) != basis.ParentJob.ProjectID || domain.ID(basis.Input.Base.ConfigRevisionID) != basis.ParentJob.RevisionID {
		return false
	}
	if !basis.LastAttempt.Valid() || !basis.Outcome.Valid() || basis.Outcome.JobID != basis.ParentJob.ID || basis.Outcome.AttemptID != basis.LastAttempt.AttemptID || basis.Outcome.ErrorCode != basis.Failure.Code || !basis.Failure.Valid() || basis.NonIdempotentToolCalls < 0 {
		return false
	}
	if basis.Outcome.Outcome == aicontract.OutcomeInterrupted {
		return basis.Failure.Class == aiprovider.ErrorInterrupted
	}
	return basis.Outcome.Outcome == aicontract.OutcomeFailed && basis.Failure.Class != aiprovider.ErrorCanceled
}

type RetrySettingsRequest struct {
	AttemptID            aicontract.AttemptID
	InputHash            aicontract.Hash
	EvidenceManifestHash aicontract.Hash
	CancelGeneration     uint64
}

func (request RetrySettingsRequest) Valid() bool {
	return request.AttemptID.Valid() && request.InputHash.Valid() && request.EvidenceManifestHash.Valid()
}

type RetrySettingsResolution struct {
	Capability aiprovider.CapabilityState
	Manifest   aiprovider.AttemptManifest
}

type ExplicitRetrySettingsSource interface {
	ResolveExplicitRetrySettings(context.Context, RetrySettingsRequest) (RetrySettingsResolution, error)
}

type ExplicitRetryIDGenerator interface {
	New() (domain.ID, error)
}

type ExplicitRetryCommand struct {
	JobID        domain.ID
	JobRequest   sharedjob.Request
	InitialPhase JobPhase
	Attempt      AttemptRecord
}

func (command ExplicitRetryCommand) Valid() bool {
	return command.JobID.Valid() && command.JobRequest.Valid() && command.JobRequest.Kind == AIJobKind && command.InitialPhase == PhaseInputPinned && command.Attempt.Valid() && command.Attempt.Kind == AttemptRetry && command.Attempt.JobID == command.JobID && command.Attempt.ParentJobID.Valid() && command.Attempt.ParentID.Valid() && command.Attempt.Manifest.InputHash == aicontract.Hash(command.JobRequest.InputHash) && command.Attempt.Manifest.CancelGeneration == 0
}

type ExplicitRetryRecord struct {
	State   AIJobState
	Attempt AttemptRecord
}

func (record ExplicitRetryRecord) Valid() bool {
	return record.State.Valid() && record.State.Job.Status == sharedjob.Queued && record.State.Phase == PhaseInputPinned && record.Attempt.Valid() && record.Attempt.Kind == AttemptRetry && record.Attempt.JobID == record.State.Job.ID && record.State.Job.InputHash == string(record.Attempt.Manifest.InputHash)
}

type ExplicitRetryRepository interface {
	LoadExplicitRetryBasis(context.Context, domain.ID) (ExplicitRetryBasis, error)
	FindExplicitRetry(context.Context, domain.ID, string) (ExplicitRetryRecord, bool, error)
	CreateExplicitRetry(context.Context, ExplicitRetryCommand) (ExplicitRetryRecord, bool, error)
}

type ExplicitRetryService struct {
	Admission  Admission
	Settings   ExplicitRetrySettingsSource
	IDs        ExplicitRetryIDGenerator
	Repository ExplicitRetryRepository
}

func (service ExplicitRetryService) Retry(ctx context.Context, request ExplicitRetryRequest) (ExplicitRetryRecord, bool, error) {
	if ctx == nil || service.Admission.Snapshots == nil || service.Settings == nil || service.IDs == nil || service.Repository == nil || !request.ParentJobID.Valid() || request.IdempotencyKey == "" || !request.UserConfirmed {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryInvalid
	}
	if existing, found, err := service.Repository.FindExplicitRetry(ctx, request.ParentJobID, request.IdempotencyKey); err != nil {
		return ExplicitRetryRecord{}, false, err
	} else if found {
		if !existing.Valid() || existing.Attempt.ParentJobID != request.ParentJobID || existing.State.Job.IdempotencyKey != request.IdempotencyKey {
			return ExplicitRetryRecord{}, false, ErrExplicitRetryConflict
		}
		return existing, true, nil
	}

	basis, err := service.Repository.LoadExplicitRetryBasis(ctx, request.ParentJobID)
	if err != nil {
		return ExplicitRetryRecord{}, false, err
	}
	if !basis.Valid() {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryInvalid
	}
	if !basis.Failure.Retryable || basis.NonIdempotentToolCalls != 0 || (basis.Failure.Class != aiprovider.ErrorTransient && basis.Failure.Class != aiprovider.ErrorTimeout && basis.Failure.Class != aiprovider.ErrorInterrupted) {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryNotRetryable
	}

	admitted, err := service.Admission.Admit(ctx, retryAdmissionRequest(basis.Input))
	if err != nil {
		return ExplicitRetryRecord{}, false, errors.Join(ErrExplicitRetryStale, err)
	}
	if admitted.InputHash != aicontract.Hash(basis.ParentJob.InputHash) || !equalAIDesignInput(admitted.Input, basis.Input) {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryStale
	}

	jobID, err := service.IDs.New()
	if err != nil || !jobID.Valid() || jobID == basis.ParentJob.ID {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryInvalid
	}
	attemptValue, err := service.IDs.New()
	attemptID := aicontract.AttemptID(attemptValue)
	if err != nil || !attemptID.Valid() || attemptValue == jobID || attemptID == basis.LastAttempt.AttemptID {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryInvalid
	}
	settingsRequest := RetrySettingsRequest{AttemptID: attemptID, InputHash: admitted.InputHash, EvidenceManifestHash: basis.LastAttempt.Manifest.EvidenceManifestHash}
	resolution, err := service.Settings.ResolveExplicitRetrySettings(ctx, settingsRequest)
	if err != nil {
		return ExplicitRetryRecord{}, false, err
	}
	if resolution.Capability != aiprovider.CapabilityAvailable && resolution.Capability != aiprovider.CapabilityDegraded {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryCapability
	}
	if !validRetryManifest(resolution.Manifest, settingsRequest) {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryCapability
	}
	attempt, err := NewAttemptRecord(jobID, AttemptRetry, 1, 0, basis.ParentJob.ID, basis.LastAttempt.AttemptID, resolution.Manifest)
	if err != nil {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryInvalid
	}
	requestHash, err := hashLedger("eco-guardian.ai-explicit-retry-request/v1", struct {
		ParentJobID     domain.ID            `json:"parent_job_id"`
		ParentAttemptID aicontract.AttemptID `json:"parent_attempt_id"`
		InputHash       aicontract.Hash      `json:"input_hash"`
	}{basis.ParentJob.ID, basis.LastAttempt.AttemptID, admitted.InputHash})
	if err != nil {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryInvalid
	}
	jobRequest := sharedjob.Request{ProjectID: basis.ParentJob.ProjectID, Kind: AIJobKind, RevisionID: basis.ParentJob.RevisionID, InputHash: string(admitted.InputHash), IdempotencyKey: request.IdempotencyKey, RequestHash: requestHash}
	command := ExplicitRetryCommand{JobID: jobID, JobRequest: jobRequest, InitialPhase: PhaseInputPinned, Attempt: attempt}
	if !command.Valid() {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryInvalid
	}
	created, replay, err := service.Repository.CreateExplicitRetry(ctx, command)
	if err != nil {
		return ExplicitRetryRecord{}, false, err
	}
	if !created.Valid() || created.State.Job.ID != command.JobID && !replay || created.Attempt.ParentJobID != basis.ParentJob.ID || created.Attempt.ParentID != basis.LastAttempt.AttemptID || !created.State.Job.Request().Equivalent(jobRequest) {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryConflict
	}
	return created, replay, nil
}

func retryAdmissionRequest(input aicontract.AIDesignInputV1) AdmissionRequest {
	budget := input.Budget
	return AdmissionRequest{
		ProjectID: input.Base.ProjectID, BaseRevisionID: input.Base.ConfigRevisionID,
		Goals: cloneGoals(input.Goals), Metrics: cloneMetrics(input.Metrics), Constraints: cloneConstraints(input.Constraints),
		AllowedTargets: cloneAllowedTargets(input.AllowedTargets), Scenes: append([]string(nil), input.Scenes...), RequestedBudget: &budget,
	}
}

func cloneAllowedTargets(values []aicontract.AllowedTarget) []aicontract.AllowedTarget {
	result := append([]aicontract.AllowedTarget(nil), values...)
	for index := range result {
		result[index].Paths = cloneAllowedPaths(result[index].Paths)
	}
	return result
}

func equalAIDesignInput(left, right aicontract.AIDesignInputV1) bool {
	leftHash, leftErr := aicontract.HashAIDesignInputV1(left)
	rightHash, rightErr := aicontract.HashAIDesignInputV1(right)
	return leftErr == nil && rightErr == nil && leftHash == rightHash
}

func validRetryManifest(manifest aiprovider.AttemptManifest, request RetrySettingsRequest) bool {
	if !manifest.Valid() || !request.Valid() || manifest.AttemptID != request.AttemptID || manifest.InputHash != request.InputHash || manifest.EvidenceManifestHash != request.EvidenceManifestHash || manifest.CancelGeneration != request.CancelGeneration {
		return false
	}
	fixture := aicontract.V1Fixture()
	if manifest.Prompt != fixture.Prompt.Identity || manifest.StructuredResponseSchema != fixture.PatchSchema.Identity || manifest.Orchestrator != fixture.Orchestrator.Identity || manifest.Budget != fixture.Budget.Identity || len(manifest.Tools) != len(fixture.Tools) {
		return false
	}
	for index, tool := range fixture.Tools {
		if manifest.Tools[index] != tool.Identity || (tool.SideEffect != "none" && tool.SideEffect != "external_read_only") {
			return false
		}
	}
	return true
}
