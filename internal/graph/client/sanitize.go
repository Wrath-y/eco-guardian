package client

import graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"

// SafeDiagnostic is the only client diagnostic shape intended for logs,
// metrics, persistence, or API errors. It cannot carry Graph records,
// credentials, provider bodies, SQL, or filesystem paths.
type SafeDiagnostic struct {
	RootRequestID, AttemptRequestID, ProviderCode, ProviderRequestID string
	Retryable                                                        bool
}

func NewSafeDiagnostic(root, attempt string, err *graphsync.ProviderError) SafeDiagnostic {
	safe := SafeProviderError(err)
	return SafeDiagnostic{RootRequestID: root, AttemptRequestID: attempt, ProviderCode: safe.Code, ProviderRequestID: safe.RequestID, Retryable: safe.Retryable}
}
