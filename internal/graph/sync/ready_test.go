package sync

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/graph/projector"
)

type readyCommitterFake struct {
	jobs     *workerJobStoreFake
	state    SyncState
	calls    int
	evidence string
}

func (f *readyCommitterFake) CommitGraphReady(_ context.Context, state SyncState, _ projector.Summary, evidence string) (SyncState, bool, error) {
	f.calls++
	f.state, f.evidence = state, evidence
	f.jobs.job.Status = JobSucceeded
	state.Pipeline, state.Generation = StateReady, state.Generation+1
	return state, false, nil
}

func TestReadyServiceCommitsExactVerificationBeforeFinalWorkerPhases(t *testing.T) {
	worker, jobID, jobs, events := newPhaseWorker(t)
	if _, _, err := worker.Start(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	for index, phase := range []WorkerPhase{PhaseValidationConfirmed, PhaseProjected, PhaseProviderCompatible, PhaseSubmitting, PhaseTaskAccepted, PhasePolling, PhaseVerifying} {
		if _, _, err := worker.Checkpoint(context.Background(), jobID, phase, (index+1)*10, "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
	states := &pipelineStateFake{state: SyncState{RevisionID: string(jobs.job.RevisionID), Pipeline: StateBuilding, LatestJobID: string(jobID), Warnings: []string{}}, found: true}
	committer := &readyCommitterFake{jobs: jobs}
	summary := projector.Summary{
		ProjectID:        string(jobs.job.ProjectID),
		RevisionID:       string(jobs.job.RevisionID),
		ConfigHash:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SchemaVersion:    projector.ProjectionSchemaVersion("1.0"),
		ProjectorVersion: projector.ProjectorVersion("v1"),
		ManifestHash:     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		NodeCount:        1,
	}
	verification := SnapshotVerification{Ready: true, Warnings: []string{"DEGRADED_VECTOR"}}
	ready, replay, err := (ReadyService{States: states, Worker: worker, Committer: committer}).Commit(context.Background(), jobID, jobs.job.RevisionID, summary, `{"graph_manifest_hash":"bbbb"}`, verification)
	if err != nil || replay || ready.Pipeline != StateReady || committer.calls != 1 || committer.evidence == "" || len(committer.state.Warnings) != 1 || committer.state.Warnings[0] != "DEGRADED_VECTOR" || jobs.job.Status != JobSucceeded || len(events.events) != 10 || events.events[8].Phase != PhaseReady || events.events[9].Phase != PhaseImpactHandoffRecorded {
		t.Fatalf("ready=%#v replay=%v committer=%#v job=%#v events=%#v err=%v", ready, replay, committer, jobs.job, events.events, err)
	}
}
