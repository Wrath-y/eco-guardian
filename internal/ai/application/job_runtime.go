package application

import (
	"context"
	"errors"

	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrJobRuntimeInvalid = errors.New("AI Job runtime projection is invalid")

type JobRuntimeRepository interface {
	aiorchestration.AIJobRepository
	ListAIJobEvents(context.Context, domain.ID, int64) ([]aiorchestration.AIJobEvent, error)
}

type JobRuntimeService struct {
	Repository JobRuntimeRepository
}

func (service JobRuntimeService) Get(ctx context.Context, id domain.ID) (aiorchestration.AIJobState, error) {
	if ctx == nil || service.Repository == nil || !id.Valid() {
		return aiorchestration.AIJobState{}, ErrJobRuntimeInvalid
	}
	state, err := service.Repository.GetAIJob(ctx, id)
	if err != nil {
		return aiorchestration.AIJobState{}, err
	}
	if !state.Valid() || state.Job.ID != id {
		return aiorchestration.AIJobState{}, ErrJobRuntimeInvalid
	}
	return state, nil
}

func (service JobRuntimeService) Cancel(ctx context.Context, id domain.ID) (aiorchestration.AIJobState, bool, error) {
	if ctx == nil || service.Repository == nil || !id.Valid() {
		return aiorchestration.AIJobState{}, false, ErrJobRuntimeInvalid
	}
	return (aiorchestration.AIJobController{Jobs: service.Repository}).RequestCancellation(ctx, id)
}

func (service JobRuntimeService) Events(ctx context.Context, id domain.ID, after int64) ([]aiorchestration.AIJobEvent, error) {
	if ctx == nil || service.Repository == nil || !id.Valid() || after < 0 {
		return nil, ErrJobRuntimeInvalid
	}
	events, err := service.Repository.ListAIJobEvents(ctx, id, after)
	if err != nil {
		return nil, err
	}
	for index, event := range events {
		if !event.Valid() || event.JobID != id || event.Ordinal <= after || index > 0 && event.Ordinal <= events[index-1].Ordinal {
			return nil, ErrJobRuntimeInvalid
		}
	}
	return events, nil
}
