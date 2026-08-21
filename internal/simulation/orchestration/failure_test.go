package orchestration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
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

type failureStoreFake struct {
	record sharedjob.Record
	code   string
	detail string
}

func (store *failureStoreFake) FailSimulationJob(_ context.Context, _ domain.ID, _ int64, code, detail string) (sharedjob.Record, bool, error) {
	store.code, store.detail = code, detail
	return store.record, false, nil
}

func TestPersistExecutionFailureUsesOnlyStableDiagnostics(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	store := &failureStoreFake{}
	if _, _, err = PersistExecutionFailure(context.Background(), store, id, 0, ExecutionFailure{Code: FailureTimeout, Detail: "runtime deadline exceeded"}); err != nil || store.code != FailureTimeout {
		t.Fatalf("code=%q detail=%q err=%v", store.code, store.detail, err)
	}
	if _, _, err = PersistExecutionFailure(context.Background(), store, id, 0, errors.New("unclassified")); !errors.Is(err, ErrSimulationJobInvalid) {
		t.Fatalf("expected invalid failure rejection, got %v", err)
	}
}

func TestClassifyExecutionErrorPersistsStableEvaluatorDiagnostic(t *testing.T) {
	classified := ClassifyExecutionError(engine.EvaluatorDiagnostic("FORMULA_DIVISION_BY_ZERO"))
	var diagnostic ExecutionFailure
	if !errors.As(classified, &diagnostic) || diagnostic.Code != string(engine.DiagnosticDivisionByZero) || diagnostic.Detail != "evaluator execution failed" {
		t.Fatalf("classified=%v", classified)
	}
	store := &failureStoreFake{}
	id := domain.ID("01948c1e-0000-7000-8000-000000000000")
	if _, _, err := PersistExecutionFailure(context.Background(), store, id, 0, classified); err != nil || store.code != string(engine.DiagnosticDivisionByZero) {
		t.Fatalf("code=%q err=%v", store.code, err)
	}
}

func TestPersistRecoveryRefusalMapsDriftAndCorruptionToStableFailures(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	store := &failureStoreFake{}
	if _, _, err = PersistRecoveryRefusal(context.Background(), store, id, 0, ErrRecoveryUnavailable); err != nil || store.code != FailureRecoveryUnavailable {
		t.Fatalf("code=%q err=%v", store.code, err)
	}
	if _, _, err = PersistRecoveryRefusal(context.Background(), store, id, 0, ErrRecoveryCorrupt); err != nil || store.code != FailureRecoveryMismatch {
		t.Fatalf("code=%q err=%v", store.code, err)
	}
}
