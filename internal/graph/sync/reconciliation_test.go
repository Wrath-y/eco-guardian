package sync

import (
	"context"
	"testing"
)

func TestReconcileMissingTaskAcceptsOnlyVerifiedTargetOrAbsence(t *testing.T) {
	expected := SnapshotExpectation{Namespace: "p", Version: "v", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if decision, err := ReconcileMissingTask(expected, nil); err != nil || !decision.Resubmit {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	snapshot := &Snapshot{Namespace: "p", Version: "v", ContentHash: expected.ContentHash, Status: "ready", QueryReady: true, Components: []Component{{Name: "graph", State: "ready"}, {Name: "fts", State: "ready"}}}
	if decision, err := ReconcileMissingTask(expected, snapshot); err != nil || !decision.AcceptExisting || decision.Resubmit {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	snapshot.ContentHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := ReconcileMissingTask(expected, snapshot); err == nil {
		t.Fatal("unverified target accepted")
	}
}

func TestReconciliationServiceAcceptsVerifiedTargetOrReplaysOnlyMissingTarget(t *testing.T) {
	for _, test := range []struct {
		name         string
		inspectErr   error
		snapshotHash string
		wantAccept   bool
		wantPut      int
		wantErr      bool
	}{
		{name: "exact target wins", snapshotHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", wantAccept: true},
		{name: "missing target replays immutable put", inspectErr: &ProviderError{Code: "SNAPSHOT_NOT_FOUND", Message: "missing", RequestID: "inspect-1", Details: map[string]any{}, Retryable: false}, wantPut: 1},
		{name: "mismatched target is not overwritten", snapshotHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			worker, jobID, jobs, events := newPhaseWorker(t)
			if _, _, err := worker.Start(context.Background(), jobID); err != nil {
				t.Fatal(err)
			}
			for index, phase := range []WorkerPhase{PhaseValidationConfirmed, PhaseProjected, PhaseProviderCompatible, PhaseSubmitting, PhaseTaskAccepted, PhasePolling} {
				if _, _, err := worker.Checkpoint(context.Background(), jobID, phase, (index+1)*10, "", "", nil); err != nil {
					t.Fatal(err)
				}
			}
			hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			snapshotHash := test.snapshotHash
			if snapshotHash == "" {
				snapshotHash = hash
			}
			states := &pipelineStateFake{state: SyncState{RevisionID: string(jobs.job.RevisionID), Pipeline: StateBuilding, LatestJobID: string(jobID), ExternalTaskID: "lost-task", ProviderTaskID: "lost-task", ProviderRequestID: "request-1", Warnings: []string{}}, found: true}
			provider := &submissionProviderFake{snapshot: Snapshot{Namespace: string(jobs.job.ProjectID), Version: string(jobs.job.RevisionID), ContentHash: snapshotHash, TaskID: "replacement-task", NodeCount: 1, Status: "ready", QueryReady: true, Components: []Component{{Name: "graph", State: "ready"}, {Name: "fts", State: "ready"}}}, inspectErr: test.inspectErr}
			request := ReconciliationRequest{JobID: jobID, RevisionID: jobs.job.RevisionID, Namespace: string(jobs.job.ProjectID), Version: string(jobs.job.RevisionID), Expectation: SnapshotExpectation{Namespace: string(jobs.job.ProjectID), Version: string(jobs.job.RevisionID), ContentHash: hash, NodeCount: 1}, Snapshot: PutSnapshotRequest{SchemaVersion: SnapshotSchemaVersion, Mode: "full", ContentHash: hash}, RequestID: "request-1"}
			decision, err := (ReconciliationService{States: states, Worker: worker, Provider: provider}).ReconcileTaskNotFound(context.Background(), request, &ProviderError{Code: "TASK_NOT_FOUND", Message: "missing", RequestID: "task-1", Details: map[string]any{}, Retryable: false})
			if (err != nil) != test.wantErr || decision.AcceptExisting != test.wantAccept || provider.calls != test.wantPut {
				t.Fatalf("decision=%#v put=%d state=%#v err=%v", decision, provider.calls, states.state, err)
			}
			if test.wantPut == 1 && (states.state.ProviderTaskID != "replacement-task" || len(events.events) != 9 || events.events[7].Phase != PhaseSubmitting || events.events[8].Phase != PhaseTaskAccepted) {
				t.Fatalf("state=%#v events=%#v", states.state, events.events)
			}
			if test.wantAccept && (len(events.events) != 8 || events.events[7].Phase != PhaseVerifying) {
				t.Fatalf("events=%#v", events.events)
			}
		})
	}
}
