package orchestration

import (
	"errors"
	"fmt"
	"time"

	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/engine"
)

const (
	FailureBudgetExceeded = "BUDGET_EXCEEDED"
	FailureTimeout        = "TIMEOUT"
)

var ErrExecutionFailure = errors.New("simulation execution failed")

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
