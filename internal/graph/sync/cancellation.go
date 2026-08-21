package sync

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrCancellationInvalid = errors.New("graph cancellation is invalid")

// CancellationService changes only the local durable Job. local-rag Tasks are
// intentionally never canceled: once an external task identity is recorded,
// cancellation means stop waiting and preserve that identity for recovery.
type CancellationService struct {
	States SyncStateStore
	Jobs   DurableJobStore
}

func (s CancellationService) Cancel(ctx context.Context, jobID domain.ID) (GraphJob, bool, error) {
	if s.States == nil || s.Jobs == nil || !jobID.Valid() {
		return GraphJob{}, false, ErrCancellationInvalid
	}
	job, err := s.Jobs.GetGraphJob(ctx, jobID)
	if err != nil {
		return GraphJob{}, false, err
	}
	state, found, err := s.States.GetGraphSyncState(ctx, job.RevisionID)
	if err != nil || !found || state.LatestJobID != string(job.ID) {
		return GraphJob{}, false, ErrCancellationInvalid
	}
	accepted := state.ExternalTaskID != "" || state.ProviderTaskID != ""
	next := JobCanceled
	if accepted {
		next = JobInterrupted
	}
	if job.Status == next {
		return job, true, nil
	}
	if job.Status != JobQueued && job.Status != JobRunning {
		return GraphJob{}, false, ErrCancellationInvalid
	}
	if intents, ok := s.Jobs.(CancellationIntentStore); ok {
		requested, _, requestErr := intents.RequestGraphCancellation(ctx, jobID)
		if requestErr != nil {
			return GraphJob{}, false, requestErr
		}
		job = requested
	}
	updated, swapped, err := s.Jobs.TransitionGraphJob(ctx, job.ID, job.Status, next, nil)
	if err != nil {
		return GraphJob{}, false, err
	}
	if swapped {
		return updated, false, nil
	}
	current, err := s.Jobs.GetGraphJob(ctx, job.ID)
	if err == nil && current.Status == next {
		return current, true, nil
	}
	return GraphJob{}, false, ErrCancellationInvalid
}
