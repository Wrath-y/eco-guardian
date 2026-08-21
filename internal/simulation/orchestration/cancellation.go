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
