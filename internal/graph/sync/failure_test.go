package sync

import (
	"context"
	"reflect"
	"testing"
)

type failureCommitterFake struct {
	state SyncState
	calls int
}

func (f *failureCommitterFake) CommitGraphFailure(_ context.Context, state SyncState, safeCode string) (SyncState, bool, error) {
	f.calls++
	f.state = state
	f.state.Pipeline, f.state.Generation, f.state.SafeError = StateFailed, state.Generation+1, safeCode
	return f.state, false, nil
}

func TestFailureServiceRecordsTerminalProviderTaskWithoutResubmission(t *testing.T) {
	_, jobID, jobs, _ := newPhaseWorker(t)
	states := &pipelineStateFake{state: SyncState{RevisionID: string(jobs.job.RevisionID), Pipeline: StateBuilding, LatestJobID: string(jobID), ExternalTaskID: "provider-task", ProviderTaskID: "provider-task", Warnings: []string{}}, found: true}
	committer := &failureCommitterFake{}
	service := FailureService{States: states, Committer: committer}
	failed, replay, err := service.FailProviderTask(context.Background(), jobID, jobs.job.RevisionID, Task{ID: "provider-task", State: "failed", Error: &ProviderError{Code: "REIMPORT_REQUIRED", Message: "raw graph text is not persisted", RequestID: "provider-request", Details: map[string]any{}, Retryable: false}})
	if err != nil || replay || committer.calls != 1 || failed.Pipeline != StateFailed || failed.SafeError != "REIMPORT_REQUIRED" || committer.state.ExternalTaskID != "provider-task" {
		t.Fatalf("failed=%#v replay=%v committer=%#v err=%v", failed, replay, committer, err)
	}
	states.state = failed
	if replayed, replay, err := service.FailProviderTask(context.Background(), jobID, jobs.job.RevisionID, Task{ID: "provider-task", State: "failed", Error: &ProviderError{Code: "REIMPORT_REQUIRED", Message: "ignored", RequestID: "provider-request", Details: map[string]any{}, Retryable: false}}); err != nil || !replay || !reflect.DeepEqual(replayed, failed) || committer.calls != 1 {
		t.Fatalf("replayed=%#v replay=%v calls=%d err=%v", replayed, replay, committer.calls, err)
	}
}
