package sync

import (
	"context"
	"reflect"
	"testing"
)

func TestCancellationIsLocalBeforeProviderAcceptance(t *testing.T) {
	_, jobID, jobs, _ := newPhaseWorker(t)
	states := &pipelineStateFake{state: SyncState{RevisionID: string(jobs.job.RevisionID), Pipeline: StateQueued, LatestJobID: string(jobID), Warnings: []string{}}, found: true}
	service := CancellationService{States: states, Jobs: jobs}
	canceled, replay, err := service.Cancel(context.Background(), jobID)
	if err != nil || replay || canceled.Status != JobCanceled || jobs.job.Status != JobCanceled {
		t.Fatalf("job=%#v replay=%v err=%v", canceled, replay, err)
	}
}

func TestCancellationAfterProviderAcceptanceStopsWaitingOnly(t *testing.T) {
	_, jobID, jobs, _ := newPhaseWorker(t)
	jobs.job.Status = JobRunning
	state := SyncState{RevisionID: string(jobs.job.RevisionID), Pipeline: StateBuilding, LatestJobID: string(jobID), ExternalTaskID: "task-accepted", ProviderTaskID: "task-accepted", Warnings: []string{}}
	states := &pipelineStateFake{state: state, found: true}
	service := CancellationService{States: states, Jobs: jobs}
	interrupted, replay, err := service.Cancel(context.Background(), jobID)
	if err != nil || replay || interrupted.Status != JobInterrupted || jobs.job.Status != JobInterrupted || !reflect.DeepEqual(states.state, state) {
		t.Fatalf("job=%#v replay=%v state=%#v err=%v", interrupted, replay, states.state, err)
	}
	if replayed, replay, err := service.Cancel(context.Background(), jobID); err != nil || !replay || replayed.Status != JobInterrupted {
		t.Fatalf("job=%#v replay=%v err=%v", replayed, replay, err)
	}
}
