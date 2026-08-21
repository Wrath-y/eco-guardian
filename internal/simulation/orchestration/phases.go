package orchestration

import (
	"context"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type Phase string

const (
	PhaseQueued         Phase = "QUEUED"
	PhaseMaterialized   Phase = "MATERIALIZED"
	PhaseSamplesRunning Phase = "SAMPLES_RUNNING"
	PhaseAggregating    Phase = "AGGREGATING"
	PhaseSealing        Phase = "SEALING"
	PhaseSucceeded      Phase = "SUCCEEDED"
)

var ErrSimulationPhase = errors.New("simulation phase transition is invalid")

func (phase Phase) Valid() bool {
	switch phase {
	case PhaseQueued, PhaseMaterialized, PhaseSamplesRunning, PhaseAggregating, PhaseSealing, PhaseSucceeded:
		return true
	}
	return false
}
func (phase Phase) CanFollow(previous Phase) bool {
	if previous == "" {
		return phase == PhaseQueued
	}
	switch previous {
	case PhaseQueued:
		return phase == PhaseMaterialized
	case PhaseMaterialized:
		return phase == PhaseSamplesRunning
	case PhaseSamplesRunning:
		return phase == PhaseAggregating
	case PhaseAggregating:
		return phase == PhaseSealing
	case PhaseSealing:
		return phase == PhaseSucceeded
	}
	return false
}

func AppendPhase(ctx context.Context, events sharedjob.EventStore, jobID domain.ID, ordinal int64, previous, next Phase, progress int, warning, safeError string, now time.Time) (sharedjob.Event, bool, error) {
	if events == nil || !jobID.Valid() || ordinal < 1 || !next.Valid() || !next.CanFollow(previous) || progress < 0 || progress > 100 || now.IsZero() {
		return sharedjob.Event{}, false, ErrSimulationPhase
	}
	return events.Append(ctx, sharedjob.Event{JobID: jobID, Ordinal: ordinal, Phase: string(next), Progress: progress, Warning: warning, SafeError: safeError, CreatedAt: now.UTC()})
}
