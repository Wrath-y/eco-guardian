package orchestration

import (
	"context"
	"errors"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
)

type StageFailure struct {
	Code            string `json:"code"`
	Retryable       bool   `json:"retryable"`
	RequestID       string `json:"request_id,omitempty"`
	RebuildRequired bool   `json:"rebuild_required,omitempty"`
}

func (failure StageFailure) Valid() bool {
	return stableEventText(failure.Code, 128) && optionalRequestID(failure.RequestID) && (!failure.RebuildRequired || !failure.Retryable)
}

type retrievalDependencyFailure interface {
	error
	RetrievalFailureCode() string
	RetrievalRetryable() bool
	RetrievalRebuildRequired() bool
	RetrievalRequestID() string
}

func MapRetrievalStageFailure(err error) StageFailure {
	if errors.Is(err, context.Canceled) {
		return StageFailure{Code: "AI_CANCELED"}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return StageFailure{Code: "AI_RETRIEVAL_TIMEOUT", Retryable: true}
	}
	var dependency retrievalDependencyFailure
	if errors.As(err, &dependency) {
		failure := StageFailure{
			Retryable: dependency.RetrievalRetryable(),
			RequestID: dependency.RetrievalRequestID(), RebuildRequired: dependency.RetrievalRebuildRequired(),
		}
		switch dependency.RetrievalFailureCode() {
		case "SNAPSHOT_INDEX_NOT_READY":
			failure.Code = "AI_SNAPSHOT_INDEX_NOT_READY"
		case "RETRIEVAL_UNAVAILABLE":
			failure.Code = "AI_RETRIEVAL_UNAVAILABLE"
		}
		if failure.Code != "" {
			if failure.Valid() {
				return failure
			}
		}
		return StageFailure{Code: "AI_RETRIEVAL_FAILED", Retryable: dependency.RetrievalRetryable()}
	}
	return StageFailure{Code: "AI_RETRIEVAL_FAILED", Retryable: true}
}

func MapProviderStageFailure(failure aiprovider.TerminalError) StageFailure {
	if !failure.Valid() {
		return StageFailure{Code: "AI_PROVIDER_FAILED", Retryable: true}
	}
	result := StageFailure{Code: failure.Code, Retryable: failure.Retryable, RequestID: aiprovider.SafeRequestID(failure.RequestID)}
	if !knownProviderFailureCode(result.Code) || !result.Valid() {
		return StageFailure{Code: "AI_PROVIDER_FAILED", Retryable: failure.Retryable}
	}
	return result
}

func (failure StageFailure) TerminalEvent(jobID domain.ID, attemptID aicontract.AttemptID, phase JobPhase, eventKey string, outcome aicontract.AttemptOutcome) (AIJobEventDraft, error) {
	draft := AIJobEventDraft{
		JobID: jobID, EventKey: eventKey, Kind: EventTerminal, Phase: phase, Progress: phase.Progress(), AttemptID: attemptID,
		Outcome: outcome, SafeErrorCode: failure.Code, Retryable: failure.Retryable, RequestID: failure.RequestID, RebuildRequired: failure.RebuildRequired,
	}
	if !failure.Valid() || outcome != aicontract.OutcomeFailed && outcome != aicontract.OutcomeInterrupted && outcome != aicontract.OutcomeCanceled || !draft.Valid() {
		return AIJobEventDraft{}, ErrAIJobEventInvalid
	}
	return draft, nil
}

func knownProviderFailureCode(code string) bool {
	switch code {
	case "AI_PROVIDER_CONFIGURATION_INVALID", "AI_BUDGET_EXCEEDED", "AI_CANCELED", "AI_PROVIDER_TIMEOUT", "AI_PROVIDER_FAILED",
		"AI_PROVIDER_AUTH_FAILED", "AI_MODEL_UNAVAILABLE", "AI_PROVIDER_REQUEST_REJECTED", "AI_PROVIDER_STREAM_UNSUPPORTED",
		"AI_PROVIDER_STREAM_INVALID", "AI_MODEL_IDENTITY_MISMATCH", "AI_PROVIDER_INTERRUPTED", "AI_OUTPUT_TRUNCATED",
		"AI_TOOL_CALL_INVALID", "AI_OUTPUT_INVALID":
		return true
	default:
		return false
	}
}
