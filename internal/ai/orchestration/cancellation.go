package orchestration

import (
	"context"
	"errors"
	"strconv"
	"sync"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var (
	ErrAIJobCallCanceled = errors.New("AI Job call was canceled before invocation")
	ErrAIJobLateResult   = errors.New("AI Job late result was ignored")
)

type AIJobEventPublisher interface {
	Publish(context.Context, AIJobEventDraft) (SSEEvent, bool, error)
}

type invocationSignalState struct {
	canceledGeneration int64
	active             map[*AIJobCallLease]context.CancelFunc
}

// InvocationCancellationRegistry bridges durable cancel generation changes to
// the contexts shared by Provider and deterministic tool calls. It remembers a
// cancellation even when it arrives before a worker registers its call lease.
type InvocationCancellationRegistry struct {
	mu   sync.Mutex
	jobs map[domain.ID]*invocationSignalState
}

func NewInvocationCancellationRegistry() *InvocationCancellationRegistry {
	return &InvocationCancellationRegistry{jobs: map[domain.ID]*invocationSignalState{}}
}

type AIJobCallLease struct {
	JobID                    domain.ID
	Owner                    string
	ObservedCancelGeneration int64
	Context                  context.Context

	registry *InvocationCancellationRegistry
	once     sync.Once
}

func (lease *AIJobCallLease) ProviderCancellation() aiprovider.ContextCancellation {
	if lease == nil {
		return aiprovider.ContextCancellation{}
	}
	return aiprovider.ContextCancellation{Context: lease.Context, CancelGeneration: uint64(lease.ObservedCancelGeneration)}
}

func (lease *AIJobCallLease) Release() {
	if lease == nil {
		return
	}
	lease.once.Do(func() {
		if lease.registry != nil {
			lease.registry.release(lease)
		}
	})
}

func (registry *InvocationCancellationRegistry) register(ctx context.Context, state AIJobState) (*AIJobCallLease, error) {
	if registry == nil || ctx == nil || !state.Valid() || state.Job.Status != sharedjob.Running || state.Job.CancelGeneration != 0 || state.Job.CancelRequestedAt != nil {
		return nil, ErrAIJobCallCanceled
	}
	callContext, cancel := context.WithCancel(ctx)
	lease := &AIJobCallLease{JobID: state.Job.ID, Owner: state.Owner, ObservedCancelGeneration: state.Job.CancelGeneration, Context: callContext, registry: registry}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.jobs == nil {
		registry.jobs = map[domain.ID]*invocationSignalState{}
	}
	signals := registry.jobs[state.Job.ID]
	if signals == nil {
		signals = &invocationSignalState{active: map[*AIJobCallLease]context.CancelFunc{}}
		registry.jobs[state.Job.ID] = signals
	}
	if signals.canceledGeneration > state.Job.CancelGeneration {
		cancel()
		return nil, ErrAIJobCallCanceled
	}
	signals.active[lease] = cancel
	return lease, nil
}

func (registry *InvocationCancellationRegistry) signal(jobID domain.ID, generation int64) {
	if registry == nil || !jobID.Valid() || generation < 1 {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.jobs == nil {
		registry.jobs = map[domain.ID]*invocationSignalState{}
	}
	signals := registry.jobs[jobID]
	if signals == nil {
		signals = &invocationSignalState{active: map[*AIJobCallLease]context.CancelFunc{}}
		registry.jobs[jobID] = signals
	}
	if generation > signals.canceledGeneration {
		signals.canceledGeneration = generation
	}
	for lease, cancel := range signals.active {
		if lease.ObservedCancelGeneration < generation {
			cancel()
		}
	}
}

func (registry *InvocationCancellationRegistry) release(lease *AIJobCallLease) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	signals := registry.jobs[lease.JobID]
	if signals == nil {
		return
	}
	if cancel := signals.active[lease]; cancel != nil {
		cancel()
		delete(signals.active, lease)
	}
	if len(signals.active) == 0 && signals.canceledGeneration == 0 {
		delete(registry.jobs, lease.JobID)
	}
}

type CancellationCoordinator struct {
	Controller AIJobController
	Signals    *InvocationCancellationRegistry
	Events     AIJobEventPublisher
}

// BeginCalls performs a durable generation check on both sides of in-memory
// registration. The returned context is the single signal passed through to
// Provider HTTP and every deterministic tool/evaluator call.
func (coordinator CancellationCoordinator) BeginCalls(ctx context.Context, current AIJobState) (*AIJobCallLease, error) {
	if ctx == nil || coordinator.Controller.Jobs == nil || coordinator.Signals == nil || !current.Valid() || current.Job.Status != sharedjob.Running || current.Job.CancelGeneration != 0 || current.Job.CancelRequestedAt != nil {
		return nil, ErrAIJobCallCanceled
	}
	latest, err := coordinator.Controller.Jobs.GetAIJob(ctx, current.Job.ID)
	if err != nil || !sameCallIdentity(latest, current) {
		return nil, ErrAIJobCallCanceled
	}
	lease, err := coordinator.Signals.register(ctx, latest)
	if err != nil {
		return nil, err
	}
	confirmed, err := coordinator.Controller.Jobs.GetAIJob(ctx, current.Job.ID)
	if err != nil || !sameCallIdentity(confirmed, latest) {
		lease.Release()
		return nil, ErrAIJobCallCanceled
	}
	return lease, nil
}

func (coordinator CancellationCoordinator) Cancel(ctx context.Context, jobID domain.ID) (AIJobState, bool, error) {
	if coordinator.Signals == nil {
		return AIJobState{}, false, ErrAIJobTransition
	}
	state, replay, err := coordinator.Controller.RequestCancellation(ctx, jobID)
	if err != nil {
		return AIJobState{}, false, err
	}
	coordinator.Signals.signal(jobID, state.Job.CancelGeneration)
	return state, replay, nil
}

// SettleCancellation maps a confirmed local/remote stop to canceled. When the
// remote service may have completed despite the signal, it seals interrupted.
func (coordinator CancellationCoordinator) SettleCancellation(ctx context.Context, lease *AIJobCallLease, remoteStopped bool) (AIJobState, bool, error) {
	if ctx == nil || lease == nil || coordinator.Controller.Jobs == nil {
		return AIJobState{}, false, ErrAIJobTransition
	}
	latest, err := coordinator.Controller.Jobs.GetAIJob(ctx, lease.JobID)
	if err != nil || !latest.Valid() || (latest.Owner != "" && latest.Owner != lease.Owner) || latest.Job.CancelGeneration <= lease.ObservedCancelGeneration || latest.Job.CancelRequestedAt == nil {
		return AIJobState{}, false, ErrAIJobTransition
	}
	settled, replay, err := coordinator.Controller.SettleCancellation(ctx, latest, remoteStopped)
	if err == nil {
		lease.Release()
	}
	return settled, replay, err
}

// AcceptResult is the final short generation check before response or tool
// output can be persisted or used to seal a DraftPatch.
func (coordinator CancellationCoordinator) AcceptResult(ctx context.Context, lease *AIJobCallLease, attemptID aicontract.AttemptID) error {
	if ctx == nil || lease == nil || coordinator.Controller.Jobs == nil || coordinator.Events == nil || !attemptID.Valid() {
		return ErrAIJobTransition
	}
	latest, err := coordinator.Controller.Jobs.GetAIJob(ctx, lease.JobID)
	if err != nil || !latest.Valid() {
		return ErrAIJobTransition
	}
	if latest.Job.Status == sharedjob.Running && latest.Owner == lease.Owner && latest.Job.CancelGeneration == lease.ObservedCancelGeneration && latest.Job.CancelRequestedAt == nil && lease.Context.Err() == nil {
		return nil
	}
	if latest.Job.CancelGeneration <= lease.ObservedCancelGeneration || latest.Job.CancelRequestedAt == nil {
		return ErrAIJobTransition
	}
	draft := AIJobEventDraft{
		JobID: latest.Job.ID, EventKey: "ignored-late-result/" + string(attemptID) + "/" + strconv.FormatInt(latest.Job.CancelGeneration, 10),
		Kind: EventIgnoredLateResult, Phase: latest.Phase, Progress: latest.Phase.Progress(), AttemptID: attemptID, Outcome: aicontract.OutcomeIgnoredLateResult,
	}
	if _, _, err := coordinator.Events.Publish(ctx, draft); err != nil {
		return err
	}
	return ErrAIJobLateResult
}

func sameCallIdentity(left, right AIJobState) bool {
	return left.Valid() && right.Valid() && left.Job.ID == right.Job.ID && left.Job.Status == right.Job.Status && left.Phase == right.Phase && left.Owner == right.Owner && left.Job.CancelGeneration == right.Job.CancelGeneration && left.Job.CancelRequestedAt == nil && right.Job.CancelRequestedAt == nil
}
