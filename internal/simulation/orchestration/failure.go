package orchestration

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/engine"
)

const (
	FailureBudgetExceeded = "BUDGET_EXCEEDED"
	FailureTimeout        = "TIMEOUT"
)

var ErrExecutionFailure = errors.New("simulation execution failed")

// FailureStore commits a terminal failure together with its durable Job event.
// Keeping this as one narrow port prevents orchestration from depending on
// SQLite while prohibiting split terminal writes.
type FailureStore interface {
	FailSimulationJob(context.Context, domain.ID, int64, string, string) (sharedjob.Record, bool, error)
}

// ExecutionFailure is a transport-neutral diagnostic. It is intentionally
// excluded from immutable result hashes because terminal failures never seal.
type ExecutionFailure struct {
	Code   string
	Detail string
}

func (failure ExecutionFailure) Error() string {
	return fmt.Sprintf("%s: %s", failure.Code, failure.Detail)
}
func (failure ExecutionFailure) Unwrap() error { return ErrExecutionFailure }

// ClassifyExecutionError turns bounded engine failures into stable, safe
// diagnostics rather than allowing a caller to publish a partial result.
func ClassifyExecutionError(err error) error {
	switch {
	case errors.Is(err, engine.ErrEventBudget):
		return ExecutionFailure{Code: FailureBudgetExceeded, Detail: "event budget exceeded"}
	case errors.Is(err, engine.ErrStepBudget):
		return ExecutionFailure{Code: FailureBudgetExceeded, Detail: "step budget exceeded"}
	default:
		return err
	}
}

// RuntimeDeadlineCheck uses only an injected wall clock. The deadline governs
// worker liveness and is never passed to the deterministic engine state.
func RuntimeDeadlineCheck(clock contract.Clock, startedAt time.Time, maxRuntimeMS int) (func() error, error) {
	if clock == nil || startedAt.IsZero() || maxRuntimeMS < 1 {
		return nil, ErrSimulationJobInvalid
	}
	deadline := startedAt.Add(time.Duration(maxRuntimeMS) * time.Millisecond)
	return func() error {
		if !clock.Now().Before(deadline) {
			return ExecutionFailure{Code: FailureTimeout, Detail: "runtime deadline exceeded"}
		}
		return nil
	}, nil
}

func PersistExecutionFailure(ctx context.Context, store FailureStore, jobID domain.ID, generation int64, failure error) (sharedjob.Record, bool, error) {
	if store == nil || !jobID.Valid() || generation < 0 {
		return sharedjob.Record{}, false, ErrSimulationJobInvalid
	}
	var diagnostic ExecutionFailure
	if !errors.As(failure, &diagnostic) || (diagnostic.Code != FailureBudgetExceeded && diagnostic.Code != FailureTimeout) || diagnostic.Detail == "" {
		return sharedjob.Record{}, false, ErrSimulationJobInvalid
	}
	return store.FailSimulationJob(ctx, jobID, generation, diagnostic.Code, diagnostic.Detail)
}
