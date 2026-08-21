package sync

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrRecoveryInvalid = errors.New("graph recovery configuration is invalid")

type RecoverableSyncStateStore interface {
	ListRecoverableGraphSyncStates(context.Context, int) ([]SyncState, error)
}

// RecoveryWork gives the execution composition the immutable Job, current
// mutable state, and final durable checkpoint. The executor resumes from that
// checkpoint rather than replaying projection or submission blindly.
type RecoveryWork struct {
	Job            GraphJob
	State          SyncState
	LastCheckpoint *GraphJobEvent
}

type RecoveryDispatcher interface {
	ResumeGraphJob(context.Context, RecoveryWork) error
	ReconcileInterruptedGraphJob(context.Context, RecoveryWork) error
}

type RecoveryAction string

const (
	RecoveryResumed     RecoveryAction = "resumed"
	RecoveryReconciled  RecoveryAction = "reconciled"
	RecoveryNotEligible RecoveryAction = "not_eligible"
)

type RecoveryResult struct {
	JobID  domain.ID
	Action RecoveryAction
}

// RecoveryService scans only durable, nonterminal Graph state. It never
// materializes business revisions or submits a Snapshot itself: saved Task
// identities are routed to reconciliation, while queued/running Jobs are
// resumed from their last immutable event checkpoint.
type RecoveryService struct {
	States     RecoverableSyncStateStore
	Jobs       DurableJobStore
	Events     JobEventStore
	Dispatcher RecoveryDispatcher
	Limit      int
}

func (s RecoveryService) Recover(ctx context.Context) ([]RecoveryResult, error) {
	if s.States == nil || s.Jobs == nil || s.Events == nil || s.Dispatcher == nil {
		return nil, ErrRecoveryInvalid
	}
	limit := s.Limit
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 1000 {
		return nil, ErrRecoveryInvalid
	}
	states, err := s.States.ListRecoverableGraphSyncStates(ctx, limit)
	if err != nil {
		return nil, err
	}
	results := make([]RecoveryResult, 0, len(states))
	for _, state := range states {
		result, err := s.recoverOne(ctx, state)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (s RecoveryService) recoverOne(ctx context.Context, state SyncState) (RecoveryResult, error) {
	if !state.Valid() || !domain.ID(state.RevisionID).Valid() || !domain.ID(state.LatestJobID).Valid() || (state.Pipeline != StateQueued && state.Pipeline != StateBuilding) {
		return RecoveryResult{}, ErrRecoveryInvalid
	}
	jobID := domain.ID(state.LatestJobID)
	job, err := s.Jobs.GetGraphJob(ctx, jobID)
	if err != nil {
		return RecoveryResult{}, err
	}
	if !job.Valid() || job.RevisionID != domain.ID(state.RevisionID) {
		return RecoveryResult{}, ErrRecoveryInvalid
	}
	events, err := s.Events.ListGraphJobEvents(ctx, job.ID, 0)
	if err != nil {
		return RecoveryResult{}, err
	}
	work := RecoveryWork{Job: job, State: state}
	if len(events) != 0 {
		last := events[len(events)-1]
		work.LastCheckpoint = &last
	}
	switch job.Status {
	case JobQueued, JobRunning:
		if err := s.Dispatcher.ResumeGraphJob(ctx, work); err != nil {
			return RecoveryResult{}, err
		}
		return RecoveryResult{JobID: job.ID, Action: RecoveryResumed}, nil
	case JobInterrupted:
		if state.ProviderTaskID == "" || state.ExternalTaskID == "" || state.ProviderTaskID != state.ExternalTaskID {
			return RecoveryResult{JobID: job.ID, Action: RecoveryNotEligible}, nil
		}
		if err := s.Dispatcher.ReconcileInterruptedGraphJob(ctx, work); err != nil {
			return RecoveryResult{}, err
		}
		return RecoveryResult{JobID: job.ID, Action: RecoveryReconciled}, nil
	default:
		return RecoveryResult{JobID: job.ID, Action: RecoveryNotEligible}, nil
	}
}
