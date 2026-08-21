package sync

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrWorkerCheckpoint = errors.New("graph worker checkpoint is invalid")

// PhaseWorker owns the durable phase protocol shared by every Graph worker.
// It deliberately has no provider dependency: callers persist a checkpoint
// before invoking an external effect and persist its next checkpoint only
// after the effect's local result has been durably recorded.
type PhaseWorker struct {
	Jobs   DurableJobStore
	Events JobEventStore
}

// Start claims a queued Job and establishes its first persisted checkpoint.
// Replaying Start for a running Job returns the existing QUEUED checkpoint.
func (w PhaseWorker) Start(ctx context.Context, jobID domain.ID) (GraphJobEvent, bool, error) {
	if w.Jobs == nil || w.Events == nil || !jobID.Valid() {
		return GraphJobEvent{}, false, ErrWorkerCheckpoint
	}
	job, err := w.Jobs.GetGraphJob(ctx, jobID)
	if err != nil {
		return GraphJobEvent{}, false, err
	}
	if job.Status == JobQueued {
		var swapped bool
		job, swapped, err = w.Jobs.TransitionGraphJob(ctx, jobID, JobQueued, JobRunning, nil)
		if err != nil {
			return GraphJobEvent{}, false, err
		}
		if !swapped {
			job, err = w.Jobs.GetGraphJob(ctx, jobID)
			if err != nil {
				return GraphJobEvent{}, false, err
			}
		}
	}
	if job.Status != JobRunning {
		return GraphJobEvent{}, false, ErrWorkerCheckpoint
	}
	return w.Checkpoint(ctx, jobID, PhaseQueued, 0, "", "", nil)
}

// Checkpoint persists one named worker phase exactly once. A matching replay
// is returned as such; phases can only advance one step at a time.
func (w PhaseWorker) Checkpoint(ctx context.Context, jobID domain.ID, phase WorkerPhase, progress int, warning, safeError string, result *GraphJobResult) (GraphJobEvent, bool, error) {
	if w.Jobs == nil || w.Events == nil || !jobID.Valid() || !phase.Valid() {
		return GraphJobEvent{}, false, ErrWorkerCheckpoint
	}
	job, err := w.Jobs.GetGraphJob(ctx, jobID)
	if err != nil {
		return GraphJobEvent{}, false, err
	}
	if job.Status != JobRunning && !(job.Status == JobSucceeded && (phase == PhaseReady || phase == PhaseImpactHandoffRecorded)) {
		return GraphJobEvent{}, false, ErrWorkerCheckpoint
	}
	events, err := w.Events.ListGraphJobEvents(ctx, jobID, 0)
	if err != nil {
		return GraphJobEvent{}, false, err
	}
	if len(events) == 0 {
		if phase != PhaseQueued {
			return GraphJobEvent{}, false, ErrWorkerCheckpoint
		}
		event := GraphJobEvent{JobID: jobID, Ordinal: 1, Phase: phase, Progress: progress, Warning: warning, SafeError: safeError, Result: result}
		return w.Events.AppendGraphJobEvent(ctx, event)
	}
	previous := events[len(events)-1]
	event := GraphJobEvent{JobID: jobID, Ordinal: previous.Ordinal + 1, Phase: phase, Progress: progress, Warning: warning, SafeError: safeError, Result: result}
	if previous.Phase == phase {
		event.Ordinal = previous.Ordinal
		if sameCheckpoint(previous, event) {
			return previous, true, nil
		}
		return GraphJobEvent{}, false, ErrWorkerCheckpoint
	}
	if !previous.Phase.CanAdvanceTo(phase) || progress < previous.Progress {
		return GraphJobEvent{}, false, ErrWorkerCheckpoint
	}
	return w.Events.AppendGraphJobEvent(ctx, event)
}

// RunEffect is the only worker helper intended for external calls. It makes
// the pre-effect phase observable before invoking effect and never records
// the post-effect phase when effect returns an error.
func (w PhaseWorker) RunEffect(ctx context.Context, jobID domain.ID, before WorkerPhase, beforeProgress int, after WorkerPhase, afterProgress int, effect func(context.Context) error) error {
	if effect == nil {
		return ErrWorkerCheckpoint
	}
	if _, _, err := w.Checkpoint(ctx, jobID, before, beforeProgress, "", "", nil); err != nil {
		return err
	}
	if err := effect(ctx); err != nil {
		return err
	}
	_, _, err := w.Checkpoint(ctx, jobID, after, afterProgress, "", "", nil)
	return err
}

func sameCheckpoint(left, right GraphJobEvent) bool {
	if left.JobID != right.JobID || left.Ordinal != right.Ordinal || left.Phase != right.Phase || left.Progress != right.Progress || left.Warning != right.Warning || left.SafeError != right.SafeError {
		return false
	}
	if left.Result == nil || right.Result == nil {
		return left.Result == nil && right.Result == nil
	}
	return *left.Result == *right.Result
}
