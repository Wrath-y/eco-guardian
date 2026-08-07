package release

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestWriteLaneSerializesOnlyWithinOneProject(t *testing.T) {
	lane := NewWriteLane()
	project, other := workerID(t), workerID(t)
	entered, releaseFirst, secondEntered := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 2)
	go func() {
		done <- lane.Within(context.Background(), project, func(context.Context) error { close(entered); <-releaseFirst; return nil })
	}()
	<-entered
	go func() {
		done <- lane.Within(context.Background(), project, func(context.Context) error { close(secondEntered); return nil })
	}()
	select {
	case <-secondEntered:
		t.Fatal("same project entered concurrently")
	default:
	}
	otherEntered := make(chan struct{})
	go func() {
		done <- lane.Within(context.Background(), other, func(context.Context) error { close(otherEntered); return nil })
	}()
	select {
	case <-otherEntered:
	case <-time.After(time.Second):
		t.Fatal("different project was blocked")
	}
	close(releaseFirst)
	<-secondEntered
	for range 3 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkerClaimsRunsAndCompletesDurableJob(t *testing.T) {
	job := workerJob(t)
	store := &workerStore{job: job}
	worker := Worker{Jobs: store, Lane: NewWriteLane()}
	result := JobResult{Type: "release", ID: workerID(t), URL: "/api/v1/releases/result"}
	completed, err := worker.Run(context.Background(), job.ID, func(_ context.Context, claimed Job) (JobResult, error) {
		if claimed.Status != JobRunning || claimed.RevisionID != job.RevisionID || claimed.InputHash != job.InputHash {
			t.Fatalf("claimed=%#v", claimed)
		}
		return result, nil
	})
	if err != nil || completed.Status != JobSucceeded || completed.Result == nil || *completed.Result != result {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	if _, err = worker.Run(context.Background(), job.ID, func(context.Context, Job) (JobResult, error) { return result, nil }); !errors.Is(err, ErrJobTerminal) {
		t.Fatalf("terminal worker run=%v", err)
	}
}

func TestReleaseCloseGuardBlocksQueuedOrRunningWork(t *testing.T) {
	guard := CloseGuard{Jobs: activeJobs(true)}
	if err := guard.Preflight(context.Background()); !errors.Is(err, ErrCloseBlocked) {
		t.Fatalf("active guard=%v", err)
	}
	if err := (CloseGuard{Jobs: activeJobs(false)}).Preflight(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerCancellationRespectsExternalAcceptanceBoundary(t *testing.T) {
	job := workerJob(t)
	store := &workerStore{job: job}
	worker := Worker{Jobs: store, Lane: NewWriteLane()}
	canceled, changed, err := worker.Cancel(context.Background(), job.ID, BeforeMandatoryBackup)
	if err != nil || !changed || canceled.Status != JobCanceled {
		t.Fatalf("canceled=%#v changed=%v err=%v", canceled, changed, err)
	}
	job = workerJob(t)
	job.Status = JobRunning
	store = &workerStore{job: job}
	worker = Worker{Jobs: store, Lane: NewWriteLane()}
	interrupted, changed, err := worker.Cancel(context.Background(), job.ID, ExternalAccepted)
	if err != nil || !changed || interrupted.Status != JobInterrupted {
		t.Fatalf("interrupted=%#v changed=%v err=%v", interrupted, changed, err)
	}
}

func TestWorkerRecoveryInterruptsAbandonedRunningJobWithoutRerunningWork(t *testing.T) {
	job := workerJob(t)
	job.Status = JobRunning
	store := &workerStore{job: job}
	worker := Worker{Jobs: store, Lane: NewWriteLane()}
	recovered, changed, err := worker.Recover(context.Background(), job.ID)
	if err != nil || !changed || recovered.Status != JobInterrupted {
		t.Fatalf("recovered=%#v changed=%v err=%v", recovered, changed, err)
	}
	if _, changed, err = worker.Recover(context.Background(), job.ID); err != nil || changed {
		t.Fatalf("second recovery changed=%v err=%v", changed, err)
	}
}

type activeJobs bool

func (a activeJobs) HasActiveReleaseJob(context.Context) (bool, error) { return bool(a), nil }

type workerStore struct {
	mu  sync.Mutex
	job Job
}

func (s *workerStore) GetReleaseJob(_ context.Context, id domain.ID) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.job.ID {
		return Job{}, errors.New("missing job")
	}
	return s.job, nil
}
func (s *workerStore) TransitionReleaseJob(_ context.Context, id domain.ID, expected, next JobStatus, result *JobResult) (Job, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != s.job.ID || s.job.Status != expected || !expected.CanTransitionTo(next) {
		return Job{}, false, nil
	}
	s.job.Status, s.job.Result, s.job.UpdatedAt = next, result, time.Now().UTC()
	return s.job, true, nil
}

func workerJob(t *testing.T) Job {
	t.Helper()
	now := time.Now().UTC()
	return Job{ID: workerID(t), ProjectID: workerID(t), RevisionID: workerID(t), InputHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "worker-job", RequestHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: JobQueued, CreatedAt: now, UpdatedAt: now}
}
func workerID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
