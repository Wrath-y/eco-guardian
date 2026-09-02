package bootstrap

import (
	"context"
	"errors"
	"sync"
	"time"

	backupapplication "github.com/zouyi/eco-guardian/internal/backup/application"
	backupintegration "github.com/zouyi/eco-guardian/internal/backup/integration"
	"github.com/zouyi/eco-guardian/internal/domain"
	graphgate "github.com/zouyi/eco-guardian/internal/graph/gate"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioning "github.com/zouyi/eco-guardian/internal/versioning"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

var errReleaseRuntimeUnavailable = errors.New("release runtime unavailable")

type releaseWork struct {
	jobID   domain.ID
	command versioningrelease.Command
}

// releaseRuntime supplies the process-owned worker callbacks intentionally
// absent from the transport and application layers. All durable business
// facts remain in the active project store.
type releaseRuntime struct {
	projects          *project.Manager
	registry          *versioninggate.Registry
	graph             graphsync.GraphProvider
	backups           *backupRuntime
	simulationVersion string
	lane              *versioningrelease.WriteLane

	mu       sync.Mutex
	cancel   context.CancelFunc
	queue    chan releaseWork
	inflight map[domain.ID]struct{}
	wait     sync.WaitGroup
	started  bool
}

func (runtime *releaseRuntime) Start(context.Context) error {
	if runtime == nil || runtime.projects == nil || runtime.registry == nil || runtime.graph == nil || runtime.backups == nil {
		return errReleaseRuntimeUnavailable
	}
	runtime.mu.Lock()
	if runtime.started {
		runtime.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime.cancel = cancel
	runtime.queue = make(chan releaseWork, 32)
	runtime.inflight = map[domain.ID]struct{}{}
	runtime.lane = versioningrelease.NewWriteLane()
	runtime.started = true
	runtime.wait.Add(1)
	runtime.mu.Unlock()
	go runtime.loop(ctx)
	return nil
}

func (runtime *releaseRuntime) Close(ctx context.Context) error {
	if runtime == nil {
		return nil
	}
	runtime.mu.Lock()
	if !runtime.started {
		runtime.mu.Unlock()
		return nil
	}
	cancel := runtime.cancel
	runtime.started = false
	runtime.mu.Unlock()
	cancel()
	done := make(chan struct{})
	go func() { runtime.wait.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runtime *releaseRuntime) Submit(ctx context.Context, command versioningrelease.Command) (versioningrelease.Job, error) {
	runtime.mu.Lock()
	started, queue := runtime.started, runtime.queue
	runtime.mu.Unlock()
	if !started {
		return versioningrelease.Job{}, errReleaseRuntimeUnavailable
	}
	s := runtime.activeStore()
	if s == nil {
		return versioningrelease.Job{}, errReleaseRuntimeUnavailable
	}
	preflight, err := runtime.preflight(s).Preflight(ctx, command)
	if err != nil {
		return versioningrelease.Job{}, err
	}
	job, _, err := s.CreateOrGetReleaseJob(ctx, versioningrelease.JobRequest{
		ProjectID: s.ProjectID(), RevisionID: command.CandidateRevisionID, InputHash: command.ConfigHash,
		IdempotencyKey: command.IdempotencyKey, RequestHash: preflight.Validated.RequestHash,
	})
	if err != nil {
		return versioningrelease.Job{}, err
	}
	select {
	case queue <- releaseWork{jobID: job.ID, command: command}:
		return job, nil
	case <-ctx.Done():
		return versioningrelease.Job{}, ctx.Err()
	}
}

func (runtime *releaseRuntime) Cancel(ctx context.Context, jobID domain.ID) (versioningrelease.Job, bool, error) {
	s := runtime.activeStore()
	if s == nil {
		return versioningrelease.Job{}, false, errReleaseRuntimeUnavailable
	}
	job, err := s.GetReleaseJob(ctx, jobID)
	if err != nil {
		return versioningrelease.Job{}, false, err
	}
	boundary := versioningrelease.ExternalAccepted
	if job.Status == versioningrelease.JobQueued {
		boundary = versioningrelease.BeforeMandatoryBackup
	}
	return (versioningrelease.Worker{Jobs: s, Lane: runtime.lane}).Cancel(ctx, jobID, boundary)
}

func (runtime *releaseRuntime) loop(ctx context.Context) {
	defer runtime.wait.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case work := <-runtime.queue:
			runtime.dispatch(ctx, work)
		}
	}
}

func (runtime *releaseRuntime) dispatch(ctx context.Context, work releaseWork) {
	runtime.mu.Lock()
	if _, exists := runtime.inflight[work.jobID]; exists || !runtime.started {
		runtime.mu.Unlock()
		return
	}
	runtime.inflight[work.jobID] = struct{}{}
	runtime.wait.Add(1)
	runtime.mu.Unlock()
	go func() {
		defer runtime.wait.Done()
		defer func() {
			runtime.mu.Lock()
			delete(runtime.inflight, work.jobID)
			runtime.mu.Unlock()
		}()
		runtime.run(ctx, work)
	}()
}

func (runtime *releaseRuntime) activeStore() *store.Store {
	if runtime == nil || runtime.projects == nil {
		return nil
	}
	handle, ok := runtime.projects.ActiveHandle()
	if !ok {
		return nil
	}
	provider, ok := handle.(interface{ Store() *store.Store })
	if !ok {
		return nil
	}
	return provider.Store()
}

func (runtime *releaseRuntime) preflight(s *store.Store) versioningrelease.PreflightService {
	return versioningrelease.PreflightService{
		Sources: s, Validation: validation.NewValidationGate(s), Registry: runtime.registry,
		Results: releaseGateResults{
			registry: runtime.registry, store: s, graph: runtime.graph, simulationVersion: runtime.simulationVersion,
			backupAvailable: func(ctx context.Context) bool { return runtime.backups.Capability(ctx).Available },
		},
	}
}

func (runtime *releaseRuntime) run(ctx context.Context, work releaseWork) {
	s := runtime.activeStore()
	if s == nil {
		return
	}
	worker := versioningrelease.Worker{Jobs: s, Lane: runtime.lane}
	_, _ = worker.Run(ctx, work.jobID, func(ctx context.Context, job versioningrelease.Job) (versioningrelease.JobResult, error) {
		preflightService := runtime.preflight(s)
		if _, err := preflightService.Recheck(ctx, versioningrelease.RecheckWorkerStart, work.command); err != nil {
			return versioningrelease.JobResult{}, err
		}
		if _, err := preflightService.Recheck(ctx, versioningrelease.RecheckBeforeBackup, work.command); err != nil {
			return versioningrelease.JobResult{}, err
		}
		backupGate := backupintegration.NewReleaseBackupGate(func() *backupapplication.Service {
			return runtime.backups.Current(runtime.projects)
		})
		backupEvidence, err := versioningrelease.PerformMandatoryBackup(ctx, backupGate, job)
		if err != nil {
			return versioningrelease.JobResult{}, err
		}
		preflight, err := preflightService.Recheck(ctx, versioningrelease.RecheckBeforeGraph, work.command)
		if err != nil {
			return versioningrelease.JobResult{}, err
		}
		manifest, err := versioning.CanonicalJSON(preflight.Results)
		if err != nil {
			return versioningrelease.JobResult{}, err
		}
		intentID, err := domain.NewID()
		if err != nil {
			return versioningrelease.JobResult{}, err
		}
		now := time.Now().UTC()
		intent := versioningrelease.Intent{
			ID: intentID, JobID: job.ID, CandidateRevisionID: job.RevisionID,
			BaselineReleaseID: work.command.BaselineReleaseID, PolicyID: work.command.PolicyID,
			GateManifest: manifest, GateManifestHash: versioning.SHA256(manifest), Confirmations: append([]versioningrelease.Confirmation(nil), work.command.Confirmations...),
			Backup: backupEvidence, PreviousReleaseID: work.command.BaselineReleaseID,
			RequestHash: job.RequestHash, IdempotencyKey: job.IdempotencyKey, Notes: work.command.Notes, Override: work.command.Override,
			Phase: versioningrelease.IntentRecorded, CreatedAt: now, UpdatedAt: now,
		}
		created, _, err := s.CreateIntent(ctx, intent)
		if err != nil {
			return versioningrelease.JobResult{}, err
		}
		intent = created
		intent, changed, err := s.TransitionIntent(ctx, intent.ID, versioningrelease.IntentRecorded, versioningrelease.IntentGraphActivating, "", "")
		if err != nil || !changed {
			return versioningrelease.JobResult{}, errReleaseRuntimeUnavailable
		}
		activation, err := versioningrelease.ActivateGraph(ctx, graphgate.ActivationAdapter{Provider: runtime.graph, Summaries: s}, job, intent)
		if err != nil {
			return versioningrelease.JobResult{}, err
		}
		intent, changed, err = s.TransitionIntent(ctx, intent.ID, versioningrelease.IntentGraphActivating, versioningrelease.IntentGraphActivated, activation.TaskID, "")
		if err != nil || !changed {
			return versioningrelease.JobResult{}, errReleaseRuntimeUnavailable
		}
		if _, err = preflightService.Recheck(ctx, versioningrelease.RecheckBeforeCommit, work.command); err != nil {
			return versioningrelease.JobResult{}, err
		}
		intent, changed, err = s.TransitionIntent(ctx, intent.ID, versioningrelease.IntentGraphActivated, versioningrelease.IntentPointerCommitting, activation.TaskID, "")
		if err != nil || !changed {
			return versioningrelease.JobResult{}, errReleaseRuntimeUnavailable
		}
		release, _, err := s.CommitActivatedIntent(ctx, intent.ID, preflight.Validated.Pointer.Generation)
		if err != nil {
			return versioningrelease.JobResult{}, err
		}
		return versioningrelease.JobResult{Type: "release", ID: release.ID, URL: "/api/v1/releases/" + string(release.ID)}, nil
	})
}
