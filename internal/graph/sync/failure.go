package sync

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrFailureInvalid = errors.New("graph provider failure is invalid")

// FailureCommitter atomically records the terminal pipeline state and the
// linked durable Job. It is deliberately separate from provider I/O, so a
// failed Task can never trigger an implicit replacement PUT.
type FailureCommitter interface {
	CommitGraphFailure(context.Context, SyncState, string) (SyncState, bool, error)
}

type FailureService struct {
	States    SyncStateStore
	Committer FailureCommitter
}

func (s FailureService) Fail(ctx context.Context, jobID, revisionID domain.ID, safeCode string) (SyncState, bool, error) {
	if s.States == nil || s.Committer == nil || !jobID.Valid() || !revisionID.Valid() || safeCode == "" || !safeDiagnostic(safeCode) {
		return SyncState{}, false, ErrFailureInvalid
	}
	state, found, err := s.States.GetGraphSyncState(ctx, revisionID)
	if err != nil || !found || state.LatestJobID != string(jobID) {
		return SyncState{}, false, ErrFailureInvalid
	}
	if state.Pipeline == StateFailed {
		if state.SafeError == safeCode {
			return state, true, nil
		}
		return SyncState{}, false, ErrFailureInvalid
	}
	if state.Pipeline != StateQueued && state.Pipeline != StateBuilding {
		return SyncState{}, false, ErrFailureInvalid
	}
	return s.Committer.CommitGraphFailure(ctx, state, safeCode)
}

// FailProviderTask accepts only local-rag's terminal failed Task state and
// persists its stable code. Provider text, details, and result bodies never
// enter Graph state or cause a same-PUT rebuild.
func (s FailureService) FailProviderTask(ctx context.Context, jobID, revisionID domain.ID, task Task) (SyncState, bool, error) {
	if task.State != "failed" || task.Error == nil || task.Error.Code == "" {
		return SyncState{}, false, ErrFailureInvalid
	}
	state, found, err := s.States.GetGraphSyncState(ctx, revisionID)
	if err != nil || !found || state.ProviderTaskID == "" || task.ID != state.ProviderTaskID {
		return SyncState{}, false, ErrFailureInvalid
	}
	return s.Fail(ctx, jobID, revisionID, task.Error.Code)
}
