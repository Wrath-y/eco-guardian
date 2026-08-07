package sqlite

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

func TestReleaseJobsAreProjectScopedAndIdempotentByCanonicalRequest(t *testing.T) {
	s := newStore(t)
	_, revision, err := s.Create(context.Background(), "tag", tagDraft("releasejob"))
	if err != nil {
		t.Fatal(err)
	}
	request := versioningrelease.JobRequest{ProjectID: s.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "release-1", RequestHash: strings.Repeat("b", 64)}
	first, reused, err := s.CreateOrGetReleaseJob(context.Background(), request)
	if err != nil || reused || !first.Valid() {
		t.Fatalf("first job=%#v reused=%v err=%v", first, reused, err)
	}
	second, reused, err := s.CreateOrGetReleaseJob(context.Background(), request)
	if err != nil || !reused || second.ID != first.ID || second.RequestHash != first.RequestHash {
		t.Fatalf("replay job=%#v reused=%v err=%v", second, reused, err)
	}
	changed := request
	changed.RequestHash = strings.Repeat("c", 64)
	if _, _, err = s.CreateOrGetReleaseJob(context.Background(), changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed canonical input error=%v", err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT count(*) FROM jobs WHERE project_uuid=? AND idempotency_key=?`, s.ProjectID(), request.IdempotencyKey).Scan(&count); err != nil || count != 1 {
		t.Fatalf("job count=%d err=%v", count, err)
	}
	wrongProject := request
	wrongProject.ProjectID = mustID(t)
	if _, _, err = s.CreateOrGetReleaseJob(context.Background(), wrongProject); !errors.Is(err, ErrReleaseJobProjectScope) {
		t.Fatalf("project scope error=%v", err)
	}
	if active, activeErr := s.HasActiveReleaseJob(context.Background()); activeErr != nil || !active {
		t.Fatalf("queued job active=%v err=%v", active, activeErr)
	}
}

func TestConcurrentReleaseRequestsAreIdempotentAndProjectLanesStayIsolated(t *testing.T) {
	s := newStore(t)
	_, revision, err := s.Create(context.Background(), "tag", tagDraft("concurrentjobs"))
	if err != nil {
		t.Fatal(err)
	}
	request := versioningrelease.JobRequest{ProjectID: s.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "concurrent-key", RequestHash: strings.Repeat("f", 64)}
	const callers = 12
	jobs := make(chan versioningrelease.Job, callers)
	errs := make(chan error, callers)
	var requests sync.WaitGroup
	for range callers {
		requests.Add(1)
		go func() {
			defer requests.Done()
			job, _, callErr := s.CreateOrGetReleaseJob(context.Background(), request)
			jobs <- job
			errs <- callErr
		}()
	}
	requests.Wait()
	close(jobs)
	close(errs)
	var id = ""
	for callErr := range errs {
		if callErr != nil {
			t.Fatal(callErr)
		}
	}
	for job := range jobs {
		if id == "" {
			id = string(job.ID)
		} else if string(job.ID) != id {
			t.Fatalf("duplicate job IDs %s and %s", id, job.ID)
		}
	}

	second := request
	second.IdempotencyKey = "concurrent-key-2"
	second.RequestHash = strings.Repeat("a", 64)
	secondJob, _, err := s.CreateOrGetReleaseJob(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	lane := versioningrelease.NewWriteLane()
	worker := versioningrelease.Worker{Jobs: s, Lane: lane}
	var active, maximum, effects int32
	run := func(context.Context, versioningrelease.Job) (versioningrelease.JobResult, error) {
		current := atomic.AddInt32(&active, 1)
		for {
			seen := atomic.LoadInt32(&maximum)
			if current <= seen || atomic.CompareAndSwapInt32(&maximum, seen, current) {
				break
			}
		}
		atomic.AddInt32(&effects, 1)
		atomic.AddInt32(&active, -1)
		return versioningrelease.JobResult{Type: "release", ID: mustID(t), URL: "/api/v1/releases/result"}, nil
	}
	var workers sync.WaitGroup
	for _, jobID := range []domain.ID{domain.ID(id), secondJob.ID} {
		workers.Add(1)
		go func(jobID domain.ID) {
			defer workers.Done()
			if _, workerErr := worker.Run(context.Background(), jobID, run); workerErr != nil {
				t.Errorf("worker=%v", workerErr)
			}
		}(jobID)
	}
	workers.Wait()
	if effects != 2 || maximum != 1 {
		t.Fatalf("effects=%d maximum same-project concurrency=%d", effects, maximum)
	}
}

func TestReleaseJobTransitionsUseDurableCompareAndSwapAndPreserveResult(t *testing.T) {
	s := newStore(t)
	_, revision, err := s.Create(context.Background(), "tag", tagDraft("jobtransition"))
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := s.CreateOrGetReleaseJob(context.Background(), versioningrelease.JobRequest{ProjectID: s.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "transition-1", RequestHash: strings.Repeat("d", 64)})
	if err != nil {
		t.Fatal(err)
	}
	running, swapped, err := s.TransitionReleaseJob(context.Background(), job.ID, versioningrelease.JobQueued, versioningrelease.JobRunning, nil)
	if err != nil || !swapped || running.Status != versioningrelease.JobRunning || running.RevisionID != revision.ID || running.InputHash != revision.ConfigHash {
		t.Fatalf("running=%#v swapped=%v err=%v", running, swapped, err)
	}
	if _, swapped, err = s.TransitionReleaseJob(context.Background(), job.ID, versioningrelease.JobQueued, versioningrelease.JobRunning, nil); err != nil || swapped {
		t.Fatalf("second claim swapped=%v err=%v", swapped, err)
	}
	result := &versioningrelease.JobResult{Type: "release", ID: mustID(t), URL: "/api/v1/releases/result"}
	succeeded, swapped, err := s.TransitionReleaseJob(context.Background(), job.ID, versioningrelease.JobRunning, versioningrelease.JobSucceeded, result)
	if err != nil || !swapped || succeeded.Status != versioningrelease.JobSucceeded || succeeded.Result == nil || *succeeded.Result != *result {
		t.Fatalf("succeeded=%#v swapped=%v err=%v", succeeded, swapped, err)
	}
	if _, _, err = s.TransitionReleaseJob(context.Background(), job.ID, versioningrelease.JobSucceeded, versioningrelease.JobFailed, nil); !errors.Is(err, ErrReleaseJobTransition) {
		t.Fatalf("terminal transition error=%v", err)
	}
}
