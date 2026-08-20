package sync

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

var ErrPipelineInvalid = errors.New("graph validation pipeline request is invalid")

type ValidationPipelineRequest struct {
	RevisionID domain.ID
	ConfigHash string
	Versions   validation.VersionManifest
}

func (r ValidationPipelineRequest) Valid() bool {
	return r.RevisionID.Valid() && validHash(r.ConfigHash) && r.Versions.Valid()
}

type SyncStateStore interface {
	GetGraphSyncState(context.Context, domain.ID) (SyncState, bool, error)
	CreateGraphSyncState(context.Context, SyncState) error
	CompareAndSwapGraphSyncState(context.Context, SyncState, SyncState) (SyncState, bool, error)
}

type FullValidationRunner interface {
	RunFullValidation(context.Context, domain.ID) error
}

// ValidationPipeline runs only the exact #6 FULL validation seam. It never
// projects, creates a Graph Job, or calls a provider before a recorded PASS.
type ValidationPipeline struct {
	States     SyncStateStore
	Validation FullValidationGate
	Runner     FullValidationRunner
}

func (p ValidationPipeline) Start(ctx context.Context, request ValidationPipelineRequest) (SyncState, error) {
	if !request.Valid() || p.States == nil || p.Validation == nil || p.Runner == nil {
		return SyncState{}, ErrPipelineInvalid
	}
	state, found, err := p.States.GetGraphSyncState(ctx, request.RevisionID)
	if err != nil {
		return SyncState{}, err
	}
	if !found {
		state = SyncState{RevisionID: string(request.RevisionID), Pipeline: StateSaved, Warnings: []string{}}
		if err = p.States.CreateGraphSyncState(ctx, state); err != nil {
			return SyncState{}, err
		}
	}
	if state.Pipeline == StateSaved {
		next := state
		next.Pipeline, next.Generation = StateValidating, state.Generation+1
		state, found, err = p.States.CompareAndSwapGraphSyncState(ctx, state, next)
		if err != nil || !found {
			return SyncState{}, err
		}
	}
	if state.Pipeline != StateValidating {
		return state, nil
	}
	result, checkErr := p.Validation.Check(ctx, request.RevisionID, request.ConfigHash, request.Versions)
	if checkErr == nil && result == validation.GatePass {
		return p.finish(ctx, state, StateQueued, "")
	}
	if checkErr == nil && result == validation.GateRequiresValidation {
		checkErr = p.Runner.RunFullValidation(ctx, request.RevisionID)
		if checkErr == nil {
			result, checkErr = p.Validation.Check(ctx, request.RevisionID, request.ConfigHash, request.Versions)
		}
	}
	if checkErr != nil || result != validation.GatePass {
		return p.finish(ctx, state, StateBlockedValidation, "VALIDATION_NOT_PASSED")
	}
	return p.finish(ctx, state, StateQueued, "")
}

func (p ValidationPipeline) finish(ctx context.Context, state SyncState, pipeline PipelineState, safeError string) (SyncState, error) {
	next := state
	next.Pipeline, next.Generation, next.SafeError = pipeline, state.Generation+1, safeError
	updated, swapped, err := p.States.CompareAndSwapGraphSyncState(ctx, state, next)
	if err != nil || !swapped {
		return SyncState{}, err
	}
	return updated, nil
}
