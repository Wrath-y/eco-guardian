package orchestration

import (
	"context"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

func RequireUncanceled(ctx context.Context, jobs sharedjob.Store, jobID domain.ID, generation int64) error {
	if jobs == nil || !jobID.Valid() || generation < 0 {
		return ErrSimulationJobInvalid
	}
	job, err := jobs.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if err = sharedjob.RequireUncanceled(job, generation); err != nil {
		return fmt.Errorf("simulation cancellation observed: %w", err)
	}
	return nil
}

// ConvergeCancellation turns a persisted cancellation intent into the shared
// terminal state. It is safe to call after every cooperative safe point.
func ConvergeCancellation(ctx context.Context, jobs sharedjob.Store, jobID domain.ID) (sharedjob.Record, bool, error) {
	if jobs == nil || !jobID.Valid() {
		return sharedjob.Record{}, false, ErrSimulationJobInvalid
	}
	record, err := jobs.GetJob(ctx, jobID)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	if record.CancelGeneration == 0 {
		return record, false, nil
	}
	if record.Status == sharedjob.Canceled {
		return record, true, nil
	}
	if record.Status.Terminal() {
		return record, false, fmt.Errorf("simulation cancellation cannot replace terminal job %s", record.Status)
	}
	updated, changed, err := jobs.Transition(ctx, record.ID, record.Status, sharedjob.Canceled, nil, record.CancelGeneration)
	return updated, changed, err
}
