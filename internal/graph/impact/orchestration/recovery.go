package orchestration

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type RecoverableJobStore interface {
	impact.JobStore
	ListRecoverableJobs(context.Context, int) ([]sharedjob.Record, error)
}

type Recovery struct {
	Jobs   RecoverableJobStore
	Worker Worker
}

type RecoveryResult struct {
	JobID    domain.ID
	ReportID domain.ID
	Error    error
}

func (r Recovery) Recover(ctx context.Context, limit int) ([]RecoveryResult, error) {
	if r.Jobs == nil || limit < 1 || limit > 1000 {
		return nil, ErrSubmitInvalid
	}
	jobs, err := r.Jobs.ListRecoverableJobs(ctx, limit)
	if err != nil {
		return nil, err
	}
	results := make([]RecoveryResult, 0, len(jobs))
	for _, job := range jobs {
		if job.Kind != JobKind {
			continue
		}
		report, runErr := r.Worker.Run(ctx, job.ID, "impact-recovery-"+string(job.ID))
		results = append(results, RecoveryResult{JobID: job.ID, ReportID: report.ID, Error: runErr})
	}
	return results, nil
}
