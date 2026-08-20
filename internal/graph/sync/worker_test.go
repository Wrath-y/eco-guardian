package sync

import (
	"context"
	"errors"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type workerJobStoreFake struct{ job GraphJob }

func (f *workerJobStoreFake) GetGraphJob(context.Context, domain.ID) (GraphJob, error) {
	return f.job, nil
}

func (f *workerJobStoreFake) TransitionGraphJob(_ context.Context, _ domain.ID, expected, next JobStatus, _ *GraphJobResult) (GraphJob, bool, error) {
	if f.job.Status != expected || !expected.CanTransitionTo(next) {
		return GraphJob{}, false, nil
	}
	f.job.Status = next
	return f.job, true, nil
}

type workerEventStoreFake struct{ events []GraphJobEvent }

func (f *workerEventStoreFake) AppendGraphJobEvent(_ context.Context, event GraphJobEvent) (GraphJobEvent, bool, error) {
	for _, existing := range f.events {
		if existing.Ordinal == event.Ordinal {
			if sameCheckpoint(existing, event) {
				return existing, true, nil
			}
			return GraphJobEvent{}, false, ErrWorkerCheckpoint
		}
	}
	f.events = append(f.events, event)
	return event, false, nil
}

func (f *workerEventStoreFake) ListGraphJobEvents(_ context.Context, _ domain.ID, after int64) ([]GraphJobEvent, error) {
	var events []GraphJobEvent
	for _, event := range f.events {
		if event.Ordinal > after {
			events = append(events, event)
		}
	}
	return events, nil
}

func newPhaseWorker(t *testing.T) (PhaseWorker, domain.ID, *workerJobStoreFake, *workerEventStoreFake) {
	t.Helper()
	jobID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	revisionID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	jobs := &workerJobStoreFake{job: GraphJob{ID: jobID, ProjectID: projectID, RevisionID: revisionID, InputHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "phase-worker", RequestHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: JobQueued}}
	events := &workerEventStoreFake{}
	return PhaseWorker{Jobs: jobs, Events: events}, jobID, jobs, events
}

func TestPhaseWorkerPersistsEveryWorkerPhaseExactlyOnce(t *testing.T) {
	worker, jobID, jobs, events := newPhaseWorker(t)
	first, replay, err := worker.Start(context.Background(), jobID)
	if err != nil || replay || first.Phase != PhaseQueued || jobs.job.Status != JobRunning {
		t.Fatalf("first=%#v replay=%v job=%#v err=%v", first, replay, jobs.job, err)
	}
	phases := []WorkerPhase{PhaseValidationConfirmed, PhaseProjected, PhaseProviderCompatible, PhaseSubmitting, PhaseTaskAccepted, PhasePolling, PhaseVerifying, PhaseReady, PhaseImpactHandoffRecorded}
	for index, phase := range phases {
		if _, replay, err = worker.Checkpoint(context.Background(), jobID, phase, (index+1)*10, "", "", nil); err != nil || replay {
			t.Fatalf("phase=%s replay=%v err=%v", phase, replay, err)
		}
	}
	last, replay, err := worker.Checkpoint(context.Background(), jobID, PhaseImpactHandoffRecorded, 90, "", "", nil)
	if err != nil || !replay || last.Ordinal != 10 || len(events.events) != 10 {
		t.Fatalf("last=%#v replay=%v events=%#v err=%v", last, replay, events.events, err)
	}
	if _, _, err = worker.Checkpoint(context.Background(), jobID, PhasePolling, 90, "", "", nil); !errors.Is(err, ErrWorkerCheckpoint) {
		t.Fatalf("out-of-order checkpoint err=%v", err)
	}
}

func TestPhaseWorkerRecordsCheckpointBeforeExternalEffect(t *testing.T) {
	worker, jobID, _, events := newPhaseWorker(t)
	if _, _, err := worker.Start(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	called := false
	err := worker.RunEffect(context.Background(), jobID, PhaseValidationConfirmed, 10, PhaseProjected, 20, func(context.Context) error {
		called = len(events.events) == 2 && events.events[1].Phase == PhaseValidationConfirmed
		return nil
	})
	if err != nil || !called || len(events.events) != 3 || events.events[2].Phase != PhaseProjected {
		t.Fatalf("called=%v events=%#v err=%v", called, events.events, err)
	}
	err = worker.RunEffect(context.Background(), jobID, PhaseProviderCompatible, 30, PhaseSubmitting, 40, func(context.Context) error {
		return errors.New("provider unavailable")
	})
	if err == nil || len(events.events) != 4 || events.events[3].Phase != PhaseProviderCompatible {
		t.Fatalf("events=%#v err=%v", events.events, err)
	}
}
