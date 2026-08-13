package client

import graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"

type ErrorKind string

const (
	ErrorBaseUnavailable  ErrorKind = "base_unavailable"
	ErrorSnapshotMissing  ErrorKind = "snapshot_missing"
	ErrorTaskMissing      ErrorKind = "task_missing"
	ErrorNotReady         ErrorKind = "not_ready"
	ErrorHashConflict     ErrorKind = "hash_conflict"
	ErrorHashMismatch     ErrorKind = "hash_mismatch"
	ErrorCapability       ErrorKind = "capability"
	ErrorStoreUnavailable ErrorKind = "store_unavailable"
	ErrorRetryExhausted   ErrorKind = "retry_exhausted"
	ErrorIntegrity        ErrorKind = "integrity"
	ErrorUnexpected       ErrorKind = "unexpected"
)

// ClassifyError is code-only. It must never branch on provider messages or
// leak provider body fields into logs, APIs, or persisted diagnostics.
func ClassifyError(err *graphsync.ProviderError) ErrorKind {
	if err == nil {
		return ErrorUnexpected
	}
	switch err.Code {
	case "BASE_SNAPSHOT_NOT_FOUND", "BASE_SNAPSHOT_NOT_READY":
		return ErrorBaseUnavailable
	case "SNAPSHOT_NOT_FOUND":
		return ErrorSnapshotMissing
	case "TASK_NOT_FOUND":
		return ErrorTaskMissing
	case "SNAPSHOT_NOT_READY", "SNAPSHOT_INDEX_NOT_READY":
		return ErrorNotReady
	case "CONTENT_HASH_CONFLICT":
		return ErrorHashConflict
	case "CONTENT_HASH_MISMATCH":
		return ErrorHashMismatch
	case "INVALID_SNAPSHOT_REQUEST", "INVALID_DELTA_OPERATION", "LIMIT_EXCEEDED":
		return ErrorCapability
	case "GRAPH_STORE_UNAVAILABLE":
		return ErrorStoreUnavailable
	case "RETRY_EXHAUSTED":
		return ErrorRetryExhausted
	case "REIMPORT_REQUIRED", "DUPLICATE_NODE_ID", "DUPLICATE_EDGE_ID", "DANGLING_EDGE", "INVALID_RELATION_PROVENANCE":
		return ErrorIntegrity
	default:
		return ErrorUnexpected
	}
}

type SafeError struct {
	Code      string
	Retryable bool
	RequestID string
	Kind      ErrorKind
}

func SafeProviderError(err *graphsync.ProviderError) SafeError {
	if err == nil {
		return SafeError{Kind: ErrorUnexpected}
	}
	return SafeError{Code: err.Code, Retryable: err.Retryable, RequestID: err.RequestID, Kind: ClassifyError(err)}
}
