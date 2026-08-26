package sync

import (
	"context"
	"reflect"
	"testing"
)

func TestGraphShutdownInterruptsOnlyRunningWrapperAndPreservesProviderTask(t *testing.T) {
	_, jobID, jobs, _ := newPhaseWorker(t)
	jobs.job.Status = JobRunning
	state := SyncState{RevisionID: string(jobs.job.RevisionID), Pipeline: StateBuilding, LatestJobID: string(jobID), ProviderTaskID: "task-original", ExternalTaskID: "task-original", Warnings: []string{}}
	states := recoverableStateStoreFake{states: []SyncState{state}}
	if err := (ShutdownService{States: states, Jobs: jobs}).PrepareShutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if jobs.job.Status != JobInterrupted || !reflect.DeepEqual(states.states[0], state) {
		t.Fatalf("job=%#v state=%#v", jobs.job, states.states[0])
	}
}

func TestLocalRAGTaskContractHasExactlyFourStates(t *testing.T) {
	for _, state := range []string{"queued", "running", "succeeded", "failed"} {
		if !(Task{State: state}).HasContractState() {
			t.Fatalf("state %q rejected", state)
		}
	}
	for _, state := range []string{"interrupted", "canceled", "cancelled", "ready", ""} {
		if (Task{State: state}).HasContractState() {
			t.Fatalf("provider-only state %q accepted", state)
		}
	}
}
