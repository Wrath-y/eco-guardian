package recovery

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestCoordinatorUsesFixedRecoveryOrderAndStartsWorkerAfterCommittedScan(t *testing.T) {
	calls := []string{}
	stage := func(value Stage) StageDescriptor {
		return StageDescriptor{
			Stage: value,
			Scanner: ScannerFunc(func(context.Context) (ScanResult, error) {
				calls = append(calls, "scan:"+string(value))
				return ScanResult{Scanned: 1, Recovered: 1}, nil
			}),
			Worker: WorkerStarterFunc(func(context.Context) error {
				calls = append(calls, "worker:"+string(value))
				return nil
			}),
		}
	}
	coordinator, err := NewCoordinator(stage(StageRemaining), stage(StageGraph), stage(StageRestoreCompatibility), stage(StageDeterministic), stage(StageRelease))
	if err != nil {
		t.Fatal(err)
	}
	results, err := coordinator.Recover(context.Background())
	if err != nil || len(results) != 5 {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	want := []string{
		"scan:restore_compatibility_migration", "worker:restore_compatibility_migration",
		"scan:release_intent_pointer", "worker:release_intent_pointer",
		"scan:graph_external_task", "worker:graph_external_task",
		"scan:deterministic_impact_simulation_risk", "worker:deterministic_impact_simulation_risk",
		"scan:remaining_queued_work", "worker:remaining_queued_work",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
	for _, result := range results {
		if !result.ScanCommitted || !result.WorkerStarted {
			t.Fatalf("stage not committed before worker: %#v", result)
		}
	}
}

func TestCoordinatorStopsBeforeWorkerWhenScanDoesNotCommit(t *testing.T) {
	wantErr := errors.New("scan failed")
	workerCalls := 0
	coordinator, err := NewCoordinator(StageDescriptor{
		Stage:   StageGraph,
		Scanner: ScannerFunc(func(context.Context) (ScanResult, error) { return ScanResult{}, wantErr }),
		Worker:  WorkerStarterFunc(func(context.Context) error { workerCalls++; return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := coordinator.Recover(context.Background())
	if !errors.Is(err, wantErr) || len(results) != 1 || results[0].ScanCommitted || workerCalls != 0 {
		t.Fatalf("results=%#v calls=%d err=%v", results, workerCalls, err)
	}
}

func TestCoordinatorReportsRecoveryRequiredWithoutChangingOrder(t *testing.T) {
	coordinator, err := NewCoordinator(StageDescriptor{Stage: StageRelease, Scanner: ScannerFunc(func(context.Context) (ScanResult, error) {
		return ScanResult{Scanned: 2, Recovered: 1, RecoveryRequired: 1}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	results, err := coordinator.Recover(context.Background())
	if err != nil || !IsRecoveryRequired(results) {
		t.Fatalf("results=%#v err=%v", results, err)
	}
}
