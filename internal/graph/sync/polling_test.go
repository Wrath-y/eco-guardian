package sync

import (
	"context"
	"testing"
)

func TestPollingVerifiesOnlySucceededExactSnapshot(t *testing.T) {
	worker, jobID, jobs, events := newPhaseWorker(t)
	if _, _, err := worker.Start(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	for index, phase := range []WorkerPhase{PhaseValidationConfirmed, PhaseProjected, PhaseProviderCompatible, PhaseSubmitting, PhaseTaskAccepted} {
		if _, _, err := worker.Checkpoint(context.Background(), jobID, phase, (index+1)*10, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	states := &pipelineStateFake{state: SyncState{RevisionID: string(jobs.job.RevisionID), Pipeline: StateBuilding, LatestJobID: string(jobID), ExternalTaskID: "task-1", ProviderTaskID: "task-1", Warnings: []string{}}, found: true}
	provider := &submissionProviderFake{task: Task{ID: "task-1", Namespace: string(jobs.job.ProjectID), SnapshotVersion: string(jobs.job.RevisionID), State: "succeeded"}, snapshot: Snapshot{Namespace: string(jobs.job.ProjectID), Version: string(jobs.job.RevisionID), ContentHash: hash, NodeCount: 1, EdgeCount: 0, Status: "ready", QueryReady: true, Components: []Component{{Name: "graph", State: "ready"}, {Name: "fts", State: "ready"}, {Name: "vector", State: "unavailable"}}}}
	result, err := (PollingService{States: states, Worker: worker, Provider: provider}).Poll(context.Background(), PollingRequest{JobID: jobID, RevisionID: jobs.job.RevisionID, Namespace: string(jobs.job.ProjectID), Version: string(jobs.job.RevisionID), Expectation: SnapshotExpectation{Namespace: string(jobs.job.ProjectID), Version: string(jobs.job.RevisionID), ContentHash: hash, NodeCount: 1, EdgeCount: 0}, RequestID: "request-1"})
	if err != nil || result.Verification == nil || !result.Verification.Ready || len(result.Verification.Warnings) != 1 || provider.taskCalls != 1 || provider.inspectCalls != 1 || len(events.events) != 8 || events.events[7].Phase != PhaseVerifying {
		t.Fatalf("result=%#v events=%#v err=%v", result, events.events, err)
	}
}
