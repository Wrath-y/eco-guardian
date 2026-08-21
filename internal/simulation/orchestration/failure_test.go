package orchestration

import (
	"errors"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/simulation/engine"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestExecutionFailureMapsBudgetsAndInjectedRuntimeDeadline(t *testing.T) {
	failure := ClassifyExecutionError(engine.ErrEventBudget)
	var diagnostic ExecutionFailure
	if !errors.As(failure, &diagnostic) || diagnostic.Code != FailureBudgetExceeded || diagnostic.Detail != "event budget exceeded" {
		t.Fatalf("failure=%v", failure)
	}
	started := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	check, err := RuntimeDeadlineCheck(fixedClock{now: started.Add(99 * time.Millisecond)}, started, 100)
	checkErr := check()
	if err != nil || checkErr != nil {
		t.Fatalf("before deadline err=%v check=%v", err, checkErr)
	}
	check, err = RuntimeDeadlineCheck(fixedClock{now: started.Add(100 * time.Millisecond)}, started, 100)
	if err != nil {
		t.Fatal(err)
	}
	failure = check()
	if !errors.As(failure, &diagnostic) || diagnostic.Code != FailureTimeout {
		t.Fatalf("failure=%v", failure)
	}
}
