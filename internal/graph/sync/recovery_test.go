package sync

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type recoverableStateStoreFake struct{ states []SyncState }

func (f recoverableStateStoreFake) ListRecoverableGraphSyncStates(context.Context, int) ([]SyncState, error) {
	return append([]SyncState(nil), f.states...), nil
}

type recoveryJobStoreFake struct{ jobs map[domain.ID]GraphJob }

func (f *recoveryJobStoreFake) GetGraphJob(_ context.Context, id domain.ID) (GraphJob, error) {
	return f.jobs[id], nil
}
func (*recoveryJobStoreFake) TransitionGraphJob(context.Context, domain.ID, JobStatus, JobStatus, *GraphJobResult) (GraphJob, bool, error) {
	return GraphJob{}, false, ErrRecoveryInvalid
}

type recoveryEventStoreFake struct{ events map[domain.ID][]GraphJobEvent }

func (f recoveryEventStoreFake) ListGraphJobEvents(_ context.Context, id domain.ID, _ int64) ([]GraphJobEvent, error) {
	return append([]GraphJobEvent(nil), f.events[id]...), nil
}
func (recoveryEventStoreFake) AppendGraphJobEvent(context.Context, GraphJobEvent) (GraphJobEvent, bool, error) {
	return GraphJobEvent{}, false, ErrRecoveryInvalid
}

type recoveryDispatcherFake struct {
	resumed, reconciled []RecoveryWork
}

func (f *recoveryDispatcherFake) ResumeGraphJob(_ context.Context, work RecoveryWork) error {
	f.resumed = append(f.resumed, work)
	return nil
}
func (f *recoveryDispatcherFake) ReconcileInterruptedGraphJob(_ context.Context, work RecoveryWork) error {
	f.reconciled = append(f.reconciled, work)
	return nil
}

func TestRecoveryResumesCommittedWorkAndReconcilesInterruptedTasks(t *testing.T) {
	projectID, _ := domain.NewID()
	queuedID, _ := domain.NewID()
	runningID, _ := domain.NewID()
	interruptedID, _ := domain.NewID()
	failedID, _ := domain.NewID()
	revisionIDs := []domain.ID{}
	for range []domain.ID{queuedID, runningID, interruptedID, failedID} {
		id, _ := domain.NewID()
		revisionIDs = append(revisionIDs, id)
	}
	job := func(id, revisionID domain.ID, status JobStatus) GraphJob {
		return GraphJob{ID: id, ProjectID: projectID, RevisionID: revisionID, InputHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: string(id), RequestHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: status}
	}
	jobs := &recoveryJobStoreFake{jobs: map[domain.ID]GraphJob{
		queuedID:      job(queuedID, revisionIDs[0], JobQueued),
		runningID:     job(runningID, revisionIDs[1], JobRunning),
		interruptedID: job(interruptedID, revisionIDs[2], JobInterrupted),
		failedID:      job(failedID, revisionIDs[3], JobFailed),
	}}
	states := recoverableStateStoreFake{states: []SyncState{
		{RevisionID: string(revisionIDs[0]), Pipeline: StateQueued, LatestJobID: string(queuedID), Warnings: []string{}},
		{RevisionID: string(revisionIDs[1]), Pipeline: StateBuilding, LatestJobID: string(runningID), Warnings: []string{}},
		{RevisionID: string(revisionIDs[2]), Pipeline: StateBuilding, LatestJobID: string(interruptedID), ExternalTaskID: "provider-task", ProviderTaskID: "provider-task", Warnings: []string{}},
		{RevisionID: string(revisionIDs[3]), Pipeline: StateBuilding, LatestJobID: string(failedID), Warnings: []string{}},
	}}
	events := recoveryEventStoreFake{events: map[domain.ID][]GraphJobEvent{
		runningID:     {{JobID: runningID, Ordinal: 1, Phase: PhaseProviderCompatible, Progress: 30}},
		interruptedID: {{JobID: interruptedID, Ordinal: 1, Phase: PhasePolling, Progress: 70}},
	}}
	dispatcher := &recoveryDispatcherFake{}
	results, err := (RecoveryService{States: states, Jobs: jobs, Events: events, Dispatcher: dispatcher}).Recover(context.Background())
	if err != nil || len(results) != 4 || results[0].Action != RecoveryResumed || results[1].Action != RecoveryResumed || results[2].Action != RecoveryReconciled || results[3].Action != RecoveryNotEligible {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	if len(dispatcher.resumed) != 2 || dispatcher.resumed[0].LastCheckpoint != nil || dispatcher.resumed[1].LastCheckpoint == nil || dispatcher.resumed[1].LastCheckpoint.Phase != PhaseProviderCompatible || len(dispatcher.reconciled) != 1 || dispatcher.reconciled[0].State.ProviderTaskID != "provider-task" || dispatcher.reconciled[0].LastCheckpoint == nil || dispatcher.reconciled[0].LastCheckpoint.Phase != PhasePolling {
		t.Fatalf("resumed=%#v reconciled=%#v", dispatcher.resumed, dispatcher.reconciled)
	}
}
