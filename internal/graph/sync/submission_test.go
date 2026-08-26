package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
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

type crashStateStore struct {
	*pipelineStateFake
	failCAS bool
}

func (store *crashStateStore) CompareAndSwapGraphSyncState(ctx context.Context, expected, next SyncState) (SyncState, bool, error) {
	if store.failCAS {
		store.failCAS = false
		return SyncState{}, false, errors.New("injected crash before task identity commit")
	}
	return store.pipelineStateFake.CompareAndSwapGraphSyncState(ctx, expected, next)
}

type idempotentSubmissionProvider struct {
	*submissionProviderFake
	effects map[string]int
}

func (provider *idempotentSubmissionProvider) PutSnapshot(_ context.Context, namespace, version string, request PutSnapshotRequest, requestID string) (Snapshot, error) {
	provider.calls++
	if provider.effects[requestID] == 0 {
		provider.effects[requestID] = 1
	}
	return provider.snapshot, nil
}

type crashEventStore struct {
	*workerEventStoreFake
	failAccepted bool
}

func (store *crashEventStore) AppendGraphJobEvent(ctx context.Context, event GraphJobEvent) (GraphJobEvent, bool, error) {
	if event.Phase == PhaseTaskAccepted && store.failAccepted {
		store.failAccepted = false
		return GraphJobEvent{}, false, errors.New("injected crash after task identity commit")
	}
	return store.workerEventStoreFake.AppendGraphJobEvent(ctx, event)
}

func submissionCrashFixture(t *testing.T) (SubmissionRequest, *workerJobStoreFake, *pipelineStateFake, *idempotentSubmissionProvider) {
	t.Helper()
	_, jobID, jobs, _ := newPhaseWorker(t)
	revisionID := jobs.job.RevisionID
	states := &pipelineStateFake{state: SyncState{RevisionID: string(revisionID), Pipeline: StateBuilding, LatestJobID: string(jobID), Warnings: []string{}}, found: true}
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	provider := &idempotentSubmissionProvider{submissionProviderFake: &submissionProviderFake{snapshot: Snapshot{Namespace: string(jobs.job.ProjectID), Version: string(revisionID), ContentHash: hash, TaskID: "provider-task", Status: "building"}}, effects: map[string]int{}}
	request := SubmissionRequest{JobID: jobID, RevisionID: revisionID, Namespace: string(jobs.job.ProjectID), Version: string(revisionID), Snapshot: PutSnapshotRequest{SchemaVersion: SnapshotSchemaVersion, Mode: "full", ContentHash: hash}, RequestID: "eco-request-crash"}
	return request, jobs, states, provider
}

func advanceSubmissionWorker(t *testing.T, worker PhaseWorker, jobID domain.ID) {
	t.Helper()
	if _, _, err := worker.Start(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	for index, phase := range []WorkerPhase{PhaseValidationConfirmed, PhaseProjected, PhaseProviderCompatible} {
		if _, _, err := worker.Checkpoint(context.Background(), jobID, phase, (index+1)*10, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCrashAfterProviderAcceptanceBeforeTaskIDSaveReplaysOneIdempotentEffect(t *testing.T) {
	request, jobs, states, provider := submissionCrashFixture(t)
	events := &workerEventStoreFake{}
	worker := PhaseWorker{Jobs: jobs, Events: events}
	advanceSubmissionWorker(t, worker, request.JobID)
	crashingStates := &crashStateStore{pipelineStateFake: states, failCAS: true}
	service := SubmissionService{States: crashingStates, Worker: worker, Provider: provider}
	if _, _, err := service.Submit(context.Background(), request); err == nil {
		t.Fatal("expected injected crash")
	}
	updated, replay, err := service.Submit(context.Background(), request)
	if err != nil || replay || provider.calls != 2 || provider.effects[request.RequestID] != 1 || updated.ProviderTaskID != "provider-task" {
		t.Fatalf("state=%#v replay=%v calls=%d effects=%v err=%v", updated, replay, provider.calls, provider.effects, err)
	}
}

func TestCrashAfterTaskIDSaveBeforeCheckpointNeverResubmitsProviderWork(t *testing.T) {
	request, jobs, states, provider := submissionCrashFixture(t)
	events := &crashEventStore{workerEventStoreFake: &workerEventStoreFake{}, failAccepted: true}
	worker := PhaseWorker{Jobs: jobs, Events: events}
	advanceSubmissionWorker(t, worker, request.JobID)
	service := SubmissionService{States: states, Worker: worker, Provider: provider}
	if _, _, err := service.Submit(context.Background(), request); err == nil {
		t.Fatal("expected injected crash")
	}
	updated, replay, err := service.Submit(context.Background(), request)
	if err != nil || !replay || provider.calls != 1 || provider.effects[request.RequestID] != 1 || updated.ProviderTaskID != "provider-task" {
		t.Fatalf("state=%#v replay=%v calls=%d effects=%v err=%v", updated, replay, provider.calls, provider.effects, err)
	}
}
