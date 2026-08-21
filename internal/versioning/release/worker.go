package release

import (
	"context"
	"errors"
	"sync"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	ErrJobOwned       = errors.New("release job is owned by another worker")
	ErrJobTerminal    = errors.New("release job is terminal")
	ErrCloseBlocked   = errors.New("release job blocks project close")
	ErrCancelBoundary = errors.New("release cancellation boundary is invalid")
)

type CancellationBoundary string

const (
	BeforeMandatoryBackup CancellationBoundary = "before_mandatory_backup"
	BeforeExternalAccept  CancellationBoundary = "before_external_accept"
	ExternalAccepted      CancellationBoundary = "external_accepted"
)

// WriteLane serializes release side effects within a project only. Different
// projects have independent lanes and no global release lock.
type WriteLane struct {
	mu    sync.Mutex
	lanes map[domain.ID]chan struct{}
}

func NewWriteLane() *WriteLane { return &WriteLane{lanes: map[domain.ID]chan struct{}{}} }

func (l *WriteLane) Within(ctx context.Context, projectID domain.ID, run func(context.Context) error) error {
	if l == nil || !projectID.Valid() || run == nil {
		return errors.New("invalid release write lane operation")
	}
	l.mu.Lock()
	queue := l.lanes[projectID]
	if queue == nil {
		queue = make(chan struct{}, 1)
		l.lanes[projectID] = queue
	}
	l.mu.Unlock()
	select {
	case queue <- struct{}{}:
		defer func() { <-queue }()
		return run(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Worker claims a queued Job using durable CAS and runs its callback under the
// project's write lane. Later saga tasks provide the callback's backup/intent
// work; this worker never changes the revision or input identity.
type Worker struct {
	Jobs DurableJobRepository
	Lane *WriteLane
}

func (w Worker) Run(ctx context.Context, jobID domain.ID, run func(context.Context, Job) (JobResult, error)) (Job, error) {
	if w.Jobs == nil || w.Lane == nil || !jobID.Valid() || run == nil {
		return Job{}, errors.New("invalid release worker")
	}
	job, err := w.Jobs.GetReleaseJob(ctx, jobID)
	if err != nil {
		return Job{}, err
	}
	var completed Job
	err = w.Lane.Within(ctx, job.ProjectID, func(ctx context.Context) error {
		current, getErr := w.Jobs.GetReleaseJob(ctx, jobID)
		if getErr != nil {
			return getErr
		}
		if current.Status != JobQueued {
			if current.Status.Valid() && !current.Status.CanTransitionTo(JobRunning) {
				return ErrJobTerminal
			}
			return ErrJobOwned
		}
		claimed, swapped, transitionErr := w.Jobs.TransitionReleaseJob(ctx, jobID, JobQueued, JobRunning, nil)
		if transitionErr != nil {
			return transitionErr
		}
		if !swapped {
			return ErrJobOwned
		}
		result, runErr := run(ctx, claimed)
		if runErr != nil {
			completed, _, transitionErr = w.Jobs.TransitionReleaseJob(ctx, jobID, JobRunning, JobFailed, nil)
			if transitionErr != nil {
				return transitionErr
			}
			return runErr
		}
		completed, swapped, transitionErr = w.Jobs.TransitionReleaseJob(ctx, jobID, JobRunning, JobSucceeded, &result)
		if transitionErr != nil {
			return transitionErr
		}
		if !swapped {
			return ErrJobOwned
		}
		return nil
	})
	if err != nil {
		return completed, err
	}
	return completed, nil
}

// Cancel honors only safe local boundaries. Once an external task has been
// accepted it is not falsely canceled; the local Job becomes interrupted for
// recovery to reconcile on restart.
func (w Worker) Cancel(ctx context.Context, jobID domain.ID, boundary CancellationBoundary) (Job, bool, error) {
	if w.Jobs == nil || !jobID.Valid() {
		return Job{}, false, ErrCancelBoundary
	}
	job, err := w.Jobs.GetReleaseJob(ctx, jobID)
	if err != nil {
		return Job{}, false, err
	}
	if job.Status != JobQueued && job.Status != JobRunning {
		return job, false, nil
	}
	var next JobStatus
	switch boundary {
	case BeforeMandatoryBackup, BeforeExternalAccept:
		next = JobCanceled
	case ExternalAccepted:
		next = JobInterrupted
	default:
		return Job{}, false, ErrCancelBoundary
	}
	if intents, ok := w.Jobs.(CancellationIntentRepository); ok {
		requested, _, requestErr := intents.RequestReleaseCancellation(ctx, jobID)
		if requestErr != nil {
			return Job{}, false, requestErr
		}
		job = requested
	}
	return w.Jobs.TransitionReleaseJob(ctx, jobID, job.Status, next, nil)
}

// Recover marks an abandoned running Job interrupted after process restart.
// It does not rerun the callback or external work; saga recovery must inspect
// durable intent/evidence before deciding whether it can safely continue.
func (w Worker) Recover(ctx context.Context, jobID domain.ID) (Job, bool, error) {
	if w.Jobs == nil || !jobID.Valid() {
		return Job{}, false, ErrJobTerminal
	}
	job, err := w.Jobs.GetReleaseJob(ctx, jobID)
	if err != nil {
		return Job{}, false, err
	}
	if job.Status != JobRunning {
		return job, false, nil
	}
	return w.Jobs.TransitionReleaseJob(ctx, jobID, JobRunning, JobInterrupted, nil)
}

// CloseGuard is passed directly to the active-project manager at composition
// time. Queued/running release Jobs keep the project open rather than letting
// a switch abandon a write lane midway through a release.
type CloseGuard struct{ Jobs ActiveJobReader }

func (g CloseGuard) Preflight(ctx context.Context) error {
	if g.Jobs == nil {
		return nil
	}
	active, err := g.Jobs.HasActiveReleaseJob(ctx)
	if err != nil {
		return err
	}
	if active {
		return ErrCloseBlocked
	}
	return nil
}
