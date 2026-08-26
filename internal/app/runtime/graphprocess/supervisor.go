package graphprocess

import (
	"context"
	"errors"
	"sync"
	"time"

	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

var (
	ErrSupervisorInvalid = errors.New("graph process supervisor configuration is invalid")
	ErrRestartExhausted  = errors.New("graph process restart policy exhausted")
	ErrReadinessDeadline = errors.New("local-rag readiness deadline exceeded")
)

type SupervisorState string

const (
	StateNotSelected      SupervisorState = "not_selected"
	StateExternal         SupervisorState = "external"
	StateStarting         SupervisorState = "starting"
	StateReady            SupervisorState = "ready"
	StateBackoff          SupervisorState = "backoff"
	StateExited           SupervisorState = "exited"
	StateRestartExhausted SupervisorState = "restart_exhausted"
	StateStopping         SupervisorState = "stopping"
)

type CommandFactory interface {
	BuildBundled(uint16) (BundledCommand, error)
}

type CommandFactoryFunc func(uint16) (BundledCommand, error)

func (factory CommandFactoryFunc) BuildBundled(port uint16) (BundledCommand, error) {
	return factory(port)
}

type Readiness interface {
	WaitReady(context.Context, string, platformprocess.LaunchGeneration) error
}

type ReadinessFunc func(context.Context, string, platformprocess.LaunchGeneration) error

func (ready ReadinessFunc) WaitReady(ctx context.Context, endpoint string, generation platformprocess.LaunchGeneration) error {
	return ready(ctx, endpoint, generation)
}

type GracefulStopper interface {
	RequestGracefulStop(context.Context, string, platformprocess.LaunchGeneration) error
}

type SupervisorPolicy struct {
	MaximumAttempts  int
	AttemptWindow    time.Duration
	InitialBackoff   time.Duration
	MaximumBackoff   time.Duration
	ReadinessTimeout time.Duration
	StableReady      time.Duration
}

type ProcessObservation struct {
	Sequence      uint64                           `json:"sequence"`
	State         SupervisorState                  `json:"state"`
	Ownership     platformprocess.Ownership        `json:"ownership"`
	Generation    platformprocess.LaunchGeneration `json:"generation,omitempty"`
	DiagnosticPID uint32                           `json:"diagnostic_pid,omitempty"`
	Endpoint      string                           `json:"endpoint,omitempty"`
	Attempt       int                              `json:"attempt,omitempty"`
	ExitCode      int                              `json:"exit_code,omitempty"`
	Reason        string                           `json:"reason,omitempty"`
	ObservedAt    time.Time                        `json:"observed_at"`
}

type ProcessObserver interface {
	PublishProcessObservation(ProcessObservation)
}

type ProcessObserverFunc func(ProcessObservation)

func (observer ProcessObserverFunc) PublishProcessObservation(value ProcessObservation) {
	observer(value)
}

type SupervisorOptions struct {
	Adapter   platformprocess.Adapter
	Job       platformprocess.JobObject
	Commands  CommandFactory
	Readiness Readiness
	Ports     PortCandidates
	Clock     platformprocess.Clock
	Backoff   platformprocess.Backoff
	Output    platformprocess.OutputSink
	Observer  ProcessObserver
	Summaries SummaryStore
	Graceful  GracefulStopper
	Policy    SupervisorPolicy
}

type Supervisor struct {
	mu             sync.Mutex
	adapter        platformprocess.Adapter
	job            platformprocess.JobObject
	commands       CommandFactory
	readiness      Readiness
	ports          PortCandidates
	clock          platformprocess.Clock
	backoff        platformprocess.Backoff
	output         platformprocess.OutputSink
	observer       ProcessObserver
	summaries      SummaryStore
	graceful       GracefulStopper
	policy         SupervisorPolicy
	observation    ProcessObservation
	current        platformprocess.OwnedProcess
	currentWait    *processWait
	attempts       []time.Time
	nextGeneration platformprocess.LaunchGeneration
	ctx            context.Context
	cancel         context.CancelFunc
	stopOnce       sync.Once
	stopErr        error
}

func NewSupervisor(options SupervisorOptions) (*Supervisor, error) {
	if options.Adapter == nil || options.Commands == nil || options.Readiness == nil || options.Ports == nil || options.Policy.MaximumAttempts < 1 || options.Policy.MaximumAttempts > 100 || options.Policy.AttemptWindow <= 0 || options.Policy.ReadinessTimeout <= 0 || options.Policy.StableReady <= 0 {
		return nil, ErrSupervisorInvalid
	}
	if options.Clock == nil {
		options.Clock = platformprocess.SystemClock{}
	}
	if options.Backoff == nil {
		options.Backoff = platformprocess.ExponentialBackoff{Initial: options.Policy.InitialBackoff, Maximum: options.Policy.MaximumBackoff}
	}
	job := options.Job
	var err error
	if job == nil {
		job, err = options.Adapter.NewKillOnCloseJob()
		if err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		adapter: options.Adapter, job: job, commands: options.Commands, readiness: options.Readiness, ports: options.Ports,
		clock: options.Clock, backoff: options.Backoff, output: options.Output, observer: options.Observer, summaries: options.Summaries, graceful: options.Graceful, policy: options.Policy,
		observation: ProcessObservation{State: StateNotSelected, Ownership: platformprocess.OwnershipNone}, ctx: ctx, cancel: cancel,
	}, nil
}

func (supervisor *Supervisor) Snapshot() ProcessObservation {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	return supervisor.observation
}

// TerminateOwned requires the exact live handle and generation held by this
// instance. Persisted summaries and diagnostic PIDs cannot establish authority.
func (supervisor *Supervisor) TerminateOwned(generation platformprocess.LaunchGeneration, code uint32) error {
	supervisor.mu.Lock()
	owned := supervisor.current
	if owned == nil || generation == 0 || owned.Generation() != generation {
		supervisor.mu.Unlock()
		return platformprocess.ErrOwnership
	}
	supervisor.mu.Unlock()
	return owned.Terminate(code)
}

// Stop is the dependency shutdown boundary. The composition root invokes it
// only after HTTP admission, durable safe points, and workers have stopped.
func (supervisor *Supervisor) Stop(ctx context.Context) error {
	supervisor.stopOnce.Do(func() {
		current := supervisor.Snapshot()
		supervisor.publish(ProcessObservation{State: StateStopping, Ownership: current.Ownership, Generation: current.Generation, DiagnosticPID: current.DiagnosticPID, Endpoint: current.Endpoint})
		supervisor.cancel()
		supervisor.mu.Lock()
		owned, tracked := supervisor.current, supervisor.currentWait
		endpoint := supervisor.observation.Endpoint
		supervisor.mu.Unlock()
		if owned == nil {
			supervisor.stopErr = supervisor.job.Close()
			return
		}
		gracefulErr := platformprocess.ErrGracefulStopUnsupported
		if supervisor.graceful != nil {
			gracefulErr = supervisor.graceful.RequestGracefulStop(ctx, endpoint, owned.Generation())
		} else {
			gracefulErr = owned.RequestStop(ctx)
		}
		if gracefulErr == nil && tracked != nil {
			select {
			case <-tracked.done:
				_ = owned.Close()
				supervisor.clearCurrent(owned)
				supervisor.stopErr = supervisor.job.Close()
				return
			case <-ctx.Done():
			}
		}
		supervisor.stopErr = supervisor.job.Close()
		_ = owned.Close()
		supervisor.clearCurrent(owned)
	})
	return supervisor.stopErr
}

func (supervisor *Supervisor) Close(ctx context.Context) error { return supervisor.Stop(ctx) }

func (supervisor *Supervisor) ObserveExternal(endpoint string) {
	supervisor.publish(ProcessObservation{State: StateExternal, Ownership: platformprocess.OwnershipExternal, Endpoint: endpoint})
}

func (supervisor *Supervisor) StartBundled(ctx context.Context, initialPort uint16) (platformprocess.OwnedProcess, string, error) {
	if initialPort == 0 {
		return nil, "", ErrSupervisorInvalid
	}
	supervisor.mu.Lock()
	if supervisor.current != nil || supervisor.observation.State == StateStopping {
		supervisor.mu.Unlock()
		return nil, "", ErrSupervisorInvalid
	}
	supervisor.mu.Unlock()
	return supervisor.launchUntilReady(ctx, initialPort)
}

type waitResult struct {
	exit platformprocess.Exit
	err  error
}

type processWait struct {
	done   chan struct{}
	result waitResult
}

func trackProcess(owned platformprocess.OwnedProcess) *processWait {
	tracked := &processWait{done: make(chan struct{})}
	go func() {
		tracked.result.exit, tracked.result.err = owned.Wait(context.Background())
		close(tracked.done)
	}()
	return tracked
}

func (supervisor *Supervisor) launchUntilReady(ctx context.Context, initialPort uint16) (platformprocess.OwnedProcess, string, error) {
	port := initialPort
	for {
		attempt, generation, ok := supervisor.beginAttempt()
		if !ok {
			supervisor.publish(ProcessObservation{State: StateRestartExhausted, Ownership: platformprocess.OwnershipBundled, Reason: "RESTART_EXHAUSTED"})
			return nil, "", ErrRestartExhausted
		}
		built, err := supervisor.commands.BuildBundled(port)
		if err != nil {
			if !supervisor.prepareRetry(ctx, attempt, "COMMAND_INVALID") {
				return nil, "", ErrRestartExhausted
			}
			port, err = supervisor.ports.NextLoopbackPort(ctx)
			if err != nil {
				supervisor.publish(ProcessObservation{State: StateRestartExhausted, Ownership: platformprocess.OwnershipBundled, Reason: "PORT_UNAVAILABLE"})
				return nil, "", ErrRestartExhausted
			}
			continue
		}
		supervisor.publish(ProcessObservation{State: StateStarting, Ownership: platformprocess.OwnershipBundled, Generation: generation, Endpoint: built.Endpoint, Attempt: attempt})
		owned, err := supervisor.adapter.CreateSuspended(ctx, built.Process, generation, supervisor.output)
		if err == nil {
			err = supervisor.job.Assign(owned)
		}
		if err == nil {
			err = owned.Resume()
		}
		if err != nil {
			if owned != nil {
				_ = owned.Terminate(1)
				_ = owned.Close()
			}
			if !supervisor.prepareRetry(ctx, attempt, "PROCESS_START_FAILED") {
				return nil, "", ErrRestartExhausted
			}
			port, err = supervisor.ports.NextLoopbackPort(ctx)
			if err != nil {
				supervisor.publish(ProcessObservation{State: StateRestartExhausted, Ownership: platformprocess.OwnershipBundled, Reason: "PORT_UNAVAILABLE"})
				return nil, "", ErrRestartExhausted
			}
			continue
		}
		supervisor.mu.Lock()
		supervisor.current = owned
		tracked := trackProcess(owned)
		supervisor.currentWait = tracked
		supervisor.mu.Unlock()
		readyContext, cancel := context.WithTimeout(ctx, supervisor.policy.ReadinessTimeout)
		ready := make(chan error, 1)
		go func() { ready <- supervisor.readiness.WaitReady(readyContext, built.Endpoint, generation) }()
		select {
		case readyErr := <-ready:
			cancel()
			if readyErr == nil {
				supervisor.publish(ProcessObservation{State: StateReady, Ownership: platformprocess.OwnershipBundled, Generation: generation, DiagnosticPID: owned.DiagnosticPID(), Endpoint: built.Endpoint, Attempt: attempt})
				go supervisor.monitor(owned, built.Endpoint, generation, supervisor.clock.Now(), tracked)
				return owned, built.Endpoint, nil
			}
			supervisor.retire(owned, tracked)
			supervisor.clearCurrent(owned)
			supervisor.publish(ProcessObservation{State: StateExited, Ownership: platformprocess.OwnershipBundled, Generation: generation, Endpoint: built.Endpoint, Attempt: attempt, Reason: "READINESS_FAILED"})
		case <-tracked.done:
			cancel()
			_ = owned.Close()
			supervisor.clearCurrent(owned)
			supervisor.publish(ProcessObservation{State: StateExited, Ownership: platformprocess.OwnershipBundled, Generation: generation, Endpoint: built.Endpoint, Attempt: attempt, ExitCode: tracked.result.exit.Code, Reason: "PROCESS_EXITED"})
		case <-readyContext.Done():
			cancel()
			supervisor.retire(owned, tracked)
			supervisor.clearCurrent(owned)
			supervisor.publish(ProcessObservation{State: StateExited, Ownership: platformprocess.OwnershipBundled, Generation: generation, Endpoint: built.Endpoint, Attempt: attempt, Reason: "READINESS_TIMEOUT"})
		}
		if !supervisor.prepareRetry(ctx, attempt, "START_RETRY") {
			return nil, "", ErrRestartExhausted
		}
		port, err = supervisor.ports.NextLoopbackPort(ctx)
		if err != nil {
			supervisor.publish(ProcessObservation{State: StateRestartExhausted, Ownership: platformprocess.OwnershipBundled, Reason: "PORT_UNAVAILABLE"})
			return nil, "", ErrRestartExhausted
		}
	}
}

func (supervisor *Supervisor) monitor(owned platformprocess.OwnedProcess, endpoint string, generation platformprocess.LaunchGeneration, readyAt time.Time, tracked *processWait) {
	<-tracked.done
	_ = owned.Close()
	supervisor.clearCurrent(owned)
	if supervisor.ctx.Err() != nil {
		return
	}
	supervisor.publish(ProcessObservation{State: StateExited, Ownership: platformprocess.OwnershipBundled, Generation: generation, Endpoint: endpoint, ExitCode: tracked.result.exit.Code, Reason: "PROCESS_EXITED"})
	if supervisor.clock.Now().Sub(readyAt) >= supervisor.policy.StableReady {
		supervisor.mu.Lock()
		supervisor.attempts = nil
		supervisor.mu.Unlock()
	}
	if supervisor.ctx.Err() != nil {
		return
	}
	if !supervisor.prepareRetry(supervisor.ctx, len(supervisor.attempts), "PROCESS_RESTART") {
		return
	}
	port, err := supervisor.ports.NextLoopbackPort(supervisor.ctx)
	if err != nil {
		supervisor.publish(ProcessObservation{State: StateRestartExhausted, Ownership: platformprocess.OwnershipBundled, Reason: "PORT_UNAVAILABLE"})
		return
	}
	_, _, _ = supervisor.launchUntilReady(supervisor.ctx, port)
}

func (supervisor *Supervisor) beginAttempt() (int, platformprocess.LaunchGeneration, bool) {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	now := supervisor.clock.Now()
	cutoff := now.Add(-supervisor.policy.AttemptWindow)
	kept := supervisor.attempts[:0]
	for _, observed := range supervisor.attempts {
		if !observed.Before(cutoff) {
			kept = append(kept, observed)
		}
	}
	supervisor.attempts = kept
	if len(supervisor.attempts) >= supervisor.policy.MaximumAttempts {
		return len(supervisor.attempts), 0, false
	}
	supervisor.attempts = append(supervisor.attempts, now)
	supervisor.nextGeneration++
	return len(supervisor.attempts), supervisor.nextGeneration, true
}

func (supervisor *Supervisor) prepareRetry(ctx context.Context, attempt int, reason string) bool {
	if ctx.Err() != nil || supervisor.ctx.Err() != nil {
		return false
	}
	supervisor.publish(ProcessObservation{State: StateBackoff, Ownership: platformprocess.OwnershipBundled, Attempt: attempt, Reason: reason})
	timer := supervisor.clock.NewTimer(supervisor.backoff.Delay(attempt))
	defer timer.Stop()
	select {
	case <-timer.C():
		return true
	case <-ctx.Done():
		return false
	case <-supervisor.ctx.Done():
		return false
	}
}

func (supervisor *Supervisor) retire(owned platformprocess.OwnedProcess, tracked *processWait) {
	_ = owned.Terminate(1)
	select {
	case <-tracked.done:
	case <-time.After(2 * time.Second):
	}
	_ = owned.Close()
}

func (supervisor *Supervisor) clearCurrent(owned platformprocess.OwnedProcess) {
	supervisor.mu.Lock()
	if supervisor.current == owned {
		supervisor.current = nil
		supervisor.currentWait = nil
	}
	supervisor.mu.Unlock()
}

func (supervisor *Supervisor) publish(next ProcessObservation) {
	supervisor.mu.Lock()
	next.Sequence = supervisor.observation.Sequence + 1
	next.ObservedAt = supervisor.clock.Now()
	supervisor.observation = next
	observer := supervisor.observer
	summaries := supervisor.summaries
	supervisor.mu.Unlock()
	if summaries != nil {
		_ = summaries.SaveProcessSummary(next)
	}
	if observer != nil {
		observer.PublishProcessObservation(next)
	}
}
