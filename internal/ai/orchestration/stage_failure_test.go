package orchestration

import (
	"context"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
)

type stageRetrievalError struct {
	code, requestID string
	retry, rebuild  bool
}

func (failure stageRetrievalError) Error() string                  { return failure.code }
func (failure stageRetrievalError) RetrievalFailureCode() string   { return failure.code }
func (failure stageRetrievalError) RetrievalRetryable() bool       { return failure.retry }
func (failure stageRetrievalError) RetrievalRebuildRequired() bool { return failure.rebuild }
func (failure stageRetrievalError) RetrievalRequestID() string     { return failure.requestID }

func TestStageFailureMapsRetrievalAndProviderWithoutPartialResult(t *testing.T) {
	tests := []struct {
		name    string
		failure StageFailure
		code    string
		retry   bool
		rebuild bool
		request string
	}{
		{"index not ready", MapRetrievalStageFailure(stageRetrievalError{code: "SNAPSHOT_INDEX_NOT_READY", rebuild: true, requestID: "rag-request_1"}), "AI_SNAPSHOT_INDEX_NOT_READY", false, true, "rag-request_1"},
		{"retrieval timeout", MapRetrievalStageFailure(context.DeadlineExceeded), "AI_RETRIEVAL_TIMEOUT", true, false, ""},
		{"provider timeout", MapProviderStageFailure(aiprovider.TerminalError{Code: "AI_PROVIDER_TIMEOUT", Class: aiprovider.ErrorTimeout, Retryable: true, Message: "safe", RequestID: "provider-request_1"}), "AI_PROVIDER_TIMEOUT", true, false, "provider-request_1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !test.failure.Valid() || test.failure.Code != test.code || test.failure.Retryable != test.retry || test.failure.RebuildRequired != test.rebuild || test.failure.RequestID != test.request {
				t.Fatalf("failure=%#v", test.failure)
			}
			event, err := test.failure.TerminalEvent(domain.ID("018f9e40-0000-7000-8000-000000000301"), aicontract.AttemptID("attempt-1"), PhaseProviderToolLoop, "terminal-1", aicontract.OutcomeFailed)
			if err != nil || !event.Valid() || event.Result != nil || event.SafeErrorCode != test.code || event.Retryable != test.retry || event.RequestID != test.request || event.RebuildRequired != test.rebuild {
				t.Fatalf("event=%#v err=%v", event, err)
			}
		})
	}
}

func TestStageFailureNormalizesUnknownAndUnsafeDependencyData(t *testing.T) {
	retrievalFailure := MapRetrievalStageFailure(stageRetrievalError{code: "PROVIDER_SECRET", requestID: "Bearer secret"})
	if retrievalFailure.Code != "AI_RETRIEVAL_FAILED" || retrievalFailure.Retryable || retrievalFailure.RequestID != "" {
		t.Fatalf("retrieval=%#v", retrievalFailure)
	}
	providerFailure := MapProviderStageFailure(aiprovider.TerminalError{Code: "UNKNOWN_PROVIDER_CODE", Class: aiprovider.ErrorPermanent, Message: "safe", RequestID: "request-1"})
	if providerFailure.Code != "AI_PROVIDER_FAILED" || providerFailure.Retryable || providerFailure.RequestID != "" {
		t.Fatalf("provider=%#v", providerFailure)
	}
}
