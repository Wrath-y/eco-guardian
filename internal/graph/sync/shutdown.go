package sync

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/domain"
)

// ShutdownService persists only the wrapping Eco Job interruption boundary.
// The GraphProvider port intentionally has no cancellation method, so an
// accepted local-rag Task cannot be mutated or assigned a fifth state.
type ShutdownService struct {
	States RecoverableSyncStateStore
	Jobs   DurableJobStore
	Limit  int
}

func (service ShutdownService) PrepareShutdown(ctx context.Context) error {
	if service.States == nil || service.Jobs == nil {
		return ErrCancellationInvalid
	}
	limit := service.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return ErrCancellationInvalid
	}
	states, err := service.States.ListRecoverableGraphSyncStates(ctx, limit)
	if err != nil {
		return err
	}
	for _, state := range states {
		job, getErr := service.Jobs.GetGraphJob(ctx, domain.ID(state.LatestJobID))
		if getErr != nil {
			return getErr
		}
		if job.Status != JobRunning {
			continue
		}
		if intents, ok := service.Jobs.(CancellationIntentStore); ok {
			requested, _, requestErr := intents.RequestGraphCancellation(ctx, job.ID)
			if requestErr != nil {
				return requestErr
			}
			job = requested
		}
		if _, changed, transitionErr := service.Jobs.TransitionGraphJob(ctx, job.ID, JobRunning, JobInterrupted, nil); transitionErr != nil || !changed {
			if transitionErr != nil {
				return transitionErr
			}
			return ErrCancellationInvalid
		}
	}
	return nil
}
