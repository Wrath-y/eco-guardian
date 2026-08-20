package sync

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrPipelineMapping = errors.New("graph pipeline state cannot be mapped")

// PipelineMapper owns only mutable orchestration state. It never writes a
// revision or derives identity from the current working configuration.
type PipelineMapper struct{ States SyncStateStore }

func (m PipelineMapper) MarkBuilding(ctx context.Context, revisionID domain.ID) (SyncState, bool, error) {
	return m.transition(ctx, revisionID, StateQueued, StateBuilding, "")
}

func (m PipelineMapper) MarkFailed(ctx context.Context, revisionID domain.ID, safeError string) (SyncState, bool, error) {
	if m.States == nil || !revisionID.Valid() || safeError == "" {
		return SyncState{}, false, ErrPipelineMapping
	}
	state, found, err := m.States.GetGraphSyncState(ctx, revisionID)
	if err != nil || !found {
		return SyncState{}, false, ErrPipelineMapping
	}
	if state.Pipeline == StateFailed {
		if state.SafeError == safeError {
			return state, true, nil
		}
		return SyncState{}, false, ErrPipelineMapping
	}
	if state.Pipeline != StateQueued && state.Pipeline != StateBuilding {
		return SyncState{}, false, ErrPipelineMapping
	}
	next := state
	next.Pipeline, next.Generation, next.SafeError = StateFailed, state.Generation+1, safeError
	updated, swapped, err := m.States.CompareAndSwapGraphSyncState(ctx, state, next)
	if err != nil {
		return SyncState{}, false, err
	}
	if swapped {
		return updated, false, nil
	}
	latest, found, err := m.States.GetGraphSyncState(ctx, revisionID)
	if err == nil && found && latest.Pipeline == StateFailed && latest.SafeError == safeError {
		return latest, true, nil
	}
	return SyncState{}, false, ErrPipelineMapping
}

func (m PipelineMapper) transition(ctx context.Context, revisionID domain.ID, from, to PipelineState, safeError string) (SyncState, bool, error) {
	if m.States == nil || !revisionID.Valid() {
		return SyncState{}, false, ErrPipelineMapping
	}
	state, found, err := m.States.GetGraphSyncState(ctx, revisionID)
	if err != nil || !found {
		return SyncState{}, false, ErrPipelineMapping
	}
	if state.Pipeline == to {
		return state, true, nil
	}
	if state.Pipeline != from {
		return SyncState{}, false, ErrPipelineMapping
	}
	next := state
	next.Pipeline, next.Generation, next.SafeError = to, state.Generation+1, safeError
	updated, swapped, err := m.States.CompareAndSwapGraphSyncState(ctx, state, next)
	if err != nil {
		return SyncState{}, false, err
	}
	if swapped {
		return updated, false, nil
	}
	latest, found, err := m.States.GetGraphSyncState(ctx, revisionID)
	if err == nil && found && latest.Pipeline == to && latest.SafeError == safeError {
		return latest, true, nil
	}
	return SyncState{}, false, ErrPipelineMapping
}

// Freshness is a computed status for one exact revision/input identity. It is
// intentionally not stored on immutable revision history.
type Freshness struct {
	Fresh   bool
	Reasons []string
}

type FreshnessInput struct {
	RevisionID domain.ID
	InputHash  string
	State      SyncState
	Job        *GraphJob
}

func ComputeFreshness(input FreshnessInput) Freshness {
	reasons := []string{}
	if !input.RevisionID.Valid() || !validHash(input.InputHash) {
		return Freshness{Reasons: []string{"INVALID_EXPECTED_IDENTITY"}}
	}
	if input.State.RevisionID != string(input.RevisionID) {
		reasons = append(reasons, "REVISION_MISMATCH")
	}
	if input.State.Pipeline != StateReady {
		reasons = append(reasons, "GRAPH_NOT_READY")
	}
	if input.State.LatestJobID == "" {
		reasons = append(reasons, "MISSING_GRAPH_JOB")
	}
	if input.Job == nil {
		reasons = append(reasons, "JOB_UNAVAILABLE")
	} else {
		if input.State.LatestJobID != string(input.Job.ID) {
			reasons = append(reasons, "LATEST_JOB_MISMATCH")
		}
		if input.Job.RevisionID != input.RevisionID || input.Job.InputHash != input.InputHash {
			reasons = append(reasons, "JOB_IDENTITY_MISMATCH")
		}
		if input.Job.Status != JobSucceeded {
			reasons = append(reasons, "JOB_NOT_SUCCEEDED")
		}
	}
	return Freshness{Fresh: len(reasons) == 0, Reasons: reasons}
}
