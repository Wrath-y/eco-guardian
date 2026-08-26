package bootstrap

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact/analysis"
	"github.com/zouyi/eco-guardian/internal/graph/impact/orchestration"
	"github.com/zouyi/eco-guardian/internal/graph/impact/planner"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	"github.com/zouyi/eco-guardian/internal/validation"
)

var errImpactRuntimeUnavailable = errors.New("impact runtime unavailable")

type impactRuntime struct {
	projects *project.Manager
	graph    *graphRuntimeDependency

	mu      sync.Mutex
	cancel  context.CancelFunc
	queue   chan domain.ID
	wait    sync.WaitGroup
	started bool
}

func (runtime *impactRuntime) Start(context.Context) error {
	if runtime == nil || runtime.projects == nil || runtime.graph == nil {
		return nil
	}
	runtime.mu.Lock()
	if runtime.started {
		runtime.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime.cancel, runtime.queue, runtime.started = cancel, make(chan domain.ID, 128), true
	runtime.wait.Add(1)
	runtime.mu.Unlock()
	go runtime.loop(ctx)
	if worker, jobs, ok := runtime.activeWorker(); ok {
		runtime.wait.Add(1)
		go func() {
			defer runtime.wait.Done()
			_, _ = (orchestration.Recovery{Jobs: jobs, Worker: worker}).Recover(ctx, 1000)
		}()
	}
	return nil
}

func (runtime *impactRuntime) Close(ctx context.Context) error {
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

func (runtime *impactRuntime) Submit(ctx context.Context, jobID domain.ID) error {
	if runtime == nil || !jobID.Valid() {
		return errImpactRuntimeUnavailable
	}
	runtime.mu.Lock()
	queue, started := runtime.queue, runtime.started
	runtime.mu.Unlock()
	if !started {
		return errImpactRuntimeUnavailable
	}
	select {
	case queue <- jobID:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errImpactRuntimeUnavailable
	}
}

func (runtime *impactRuntime) loop(ctx context.Context) {
	defer runtime.wait.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runtime.dispatchHandoffs(ctx)
		case jobID := <-runtime.queue:
			worker, _, ok := runtime.activeWorker()
			if !ok {
				continue
			}
			_, _ = worker.Run(ctx, jobID, "impact-worker-"+uuid.NewString())
		}
	}
}

func (runtime *impactRuntime) dispatchHandoffs(ctx context.Context) {
	worker, s, ok := runtime.activeWorker()
	projectInfo, projectFound := runtime.projects.Current()
	if !ok || !projectFound {
		return
	}
	scheduler := orchestration.Scheduler{ProjectID: projectInfo.ID, Baselines: s, Admission: *worker.Admission, Submitter: orchestration.Submitter{Jobs: s, Events: s, Reports: s, Clock: worker.Clock}, Handoffs: s, Start: runtime.Submit}
	_, _ = (graphsync.ImpactRecoveryService{Store: s, Scheduler: scheduler, Limit: 100}).Recover(ctx)
}

func (runtime *impactRuntime) activeWorker() (orchestration.Worker, *store.Store, bool) {
	handle, ok := runtime.projects.ActiveHandle()
	if !ok {
		return orchestration.Worker{}, nil, false
	}
	provider, ok := handle.(interface{ Store() *store.Store })
	if !ok || provider.Store() == nil {
		return orchestration.Worker{}, nil, false
	}
	s := provider.Store()
	clock := impactRuntimeClock{}
	admission := &planner.Admission{Revisions: s, Validation: validation.NewValidationGate(s), Summaries: s, Graph: runtime.graph}
	worker := orchestration.Worker{Admission: admission, Revisions: s, Diffs: s, Provider: runtime.graph, Reports: s, Jobs: s, Events: s, IDs: impactRuntimeIDs{}, Clock: clock, Projector: projector.Descriptor{SchemaVersion: projector.ProjectionSchemaV1, Version: projector.ProjectorV1, Relations: projector.V1Relations(), Formatter: projector.V1Formatter{}}, PathOptions: analysis.PathOptions{Concurrency: 4}}
	return worker, s, true
}

type impactRuntimeClock struct{}

func (impactRuntimeClock) Now() time.Time { return time.Now().UTC() }

type impactRuntimeIDs struct{}

func (impactRuntimeIDs) New() (domain.ID, error) { return domain.NewID() }

var _ Worker = (*impactRuntime)(nil)
