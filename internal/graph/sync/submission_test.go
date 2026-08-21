package sync

import (
	"context"
	"testing"
)

type submissionProviderFake struct {
	calls, taskCalls, inspectCalls, activationCalls int
	snapshot                                        Snapshot
	task                                            Task
	inspectErr                                      error
	taskErr                                         error
}

func (f *submissionProviderFake) Health(context.Context, string) (Health, error) {
	return Health{}, nil
}
func (f *submissionProviderFake) InspectSnapshot(context.Context, string, string, string) (Snapshot, error) {
	f.inspectCalls++
	return f.snapshot, f.inspectErr
}
func (f *submissionProviderFake) PutSnapshot(_ context.Context, namespace, version string, request PutSnapshotRequest, _ string) (Snapshot, error) {
	f.calls++
	return f.snapshot, nil
}
func (f *submissionProviderFake) GetTask(context.Context, string, string) (Task, error) {
	f.taskCalls++
	return f.task, f.taskErr
}
func (f *submissionProviderFake) ActivateSnapshot(context.Context, string, string, string) (Activation, error) {
	f.activationCalls++
	return Activation{}, nil
}
func (f *submissionProviderFake) DeleteSnapshotForRetry(context.Context, string, string, string) error {
	return nil
}

func TestSubmissionPersistsTaskIdentityBeforeAcceptedCheckpoint(t *testing.T) {
	worker, jobID, jobs, events := newPhaseWorker(t)
	if _, _, err := worker.Start(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	for index, phase := range []WorkerPhase{PhaseValidationConfirmed, PhaseProjected, PhaseProviderCompatible} {
		if _, _, err := worker.Checkpoint(context.Background(), jobID, phase, (index+1)*10, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	states := &pipelineStateFake{state: SyncState{RevisionID: string(jobs.job.RevisionID), Pipeline: StateBuilding, LatestJobID: string(jobID), Warnings: []string{}}, found: true}
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	provider := &submissionProviderFake{snapshot: Snapshot{Namespace: string(jobs.job.ProjectID), Version: string(jobs.job.RevisionID), ContentHash: hash, TaskID: "provider-task", Status: "building"}}
	service := SubmissionService{States: states, Worker: worker, Provider: provider}
	request := SubmissionRequest{JobID: jobID, RevisionID: jobs.job.RevisionID, Namespace: string(jobs.job.ProjectID), Version: string(jobs.job.RevisionID), Snapshot: PutSnapshotRequest{SchemaVersion: SnapshotSchemaVersion, Mode: "full", ContentHash: hash}, RequestID: "eco-request-1"}
	updated, replay, err := service.Submit(context.Background(), request)
	if err != nil || replay || provider.calls != 1 || provider.activationCalls != 0 || updated.ProviderTaskID != "provider-task" || updated.ExternalTaskID != "provider-task" || updated.ProviderRequestID != request.RequestID || len(events.events) != 6 || events.events[5].Phase != PhaseTaskAccepted {
		t.Fatalf("state=%#v replay=%v events=%#v calls=%d err=%v", updated, replay, events.events, provider.calls, err)
	}
	if replayed, replay, err := service.Submit(context.Background(), request); err != nil || !replay || replayed.ProviderTaskID != "provider-task" || provider.calls != 1 {
		t.Fatalf("state=%#v replay=%v calls=%d err=%v", replayed, replay, provider.calls, err)
	}
}
