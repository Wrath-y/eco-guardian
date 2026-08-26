package graphprocess

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

type supervisorProcess struct {
	generation    platformprocess.LaunchGeneration
	wait          chan platformprocess.Exit
	once          sync.Once
	assigned      bool
	resumed       bool
	terminated    bool
	closed        atomic.Bool
	stopRequested bool
	stopExits     bool
}

func newSupervisorProcess() *supervisorProcess {
	return &supervisorProcess{wait: make(chan platformprocess.Exit, 1)}
}
func (*supervisorProcess) DiagnosticPID() uint32 { return 42 }
func (process *supervisorProcess) Generation() platformprocess.LaunchGeneration {
	return process.generation
}
func (process *supervisorProcess) Resume() error {
	if !process.assigned {
		return platformprocess.ErrOwnership
	}
	process.resumed = true
	return nil
}
func (process *supervisorProcess) Wait(ctx context.Context) (platformprocess.Exit, error) {
	select {
	case exit := <-process.wait:
		return exit, nil
	case <-ctx.Done():
		return platformprocess.Exit{}, ctx.Err()
	}
}
func (process *supervisorProcess) RequestStop(context.Context) error {
	process.stopRequested = true
	if process.stopExits {
		process.exit(0)
	}
	return nil
}
func (process *supervisorProcess) Terminate(code uint32) error {
	process.terminated = true
	process.once.Do(func() { process.wait <- platformprocess.Exit{Generation: process.generation, Code: int(code)} })
	return nil
}
func (process *supervisorProcess) Close() error { process.closed.Store(true); return nil }
func (process *supervisorProcess) exit(code int) {
	process.once.Do(func() { process.wait <- platformprocess.Exit{Generation: process.generation, Code: code} })
}

type supervisorAdapter struct {
	processes   []*supervisorProcess
	generations []platformprocess.LaunchGeneration
}

func (adapter *supervisorAdapter) CreateSuspended(_ context.Context, _ platformprocess.Command, generation platformprocess.LaunchGeneration, _ platformprocess.OutputSink) (platformprocess.OwnedProcess, error) {
	if len(adapter.processes) == 0 {
		return nil, errors.New("no process")
	}
	process := adapter.processes[0]
	adapter.processes = adapter.processes[1:]
	process.generation = generation
	adapter.generations = append(adapter.generations, generation)
	return process, nil
}
func (*supervisorAdapter) NewKillOnCloseJob() (platformprocess.JobObject, error) {
	return &supervisorJob{}, nil
}

type supervisorJob struct{ closed bool }

func (*supervisorJob) Assign(candidate platformprocess.OwnedProcess) error {
	candidate.(*supervisorProcess).assigned = true
	return nil
}
func (job *supervisorJob) Close() error { job.closed = true; return nil }

type instantTimer struct{ channel chan time.Time }

func newInstantTimer() *instantTimer {
	channel := make(chan time.Time, 1)
	channel <- time.Now()
	return &instantTimer{channel: channel}
}
func (timer *instantTimer) C() <-chan time.Time { return timer.channel }
func (*instantTimer) Stop() bool                { return true }

type supervisorClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *supervisorClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}
func (*supervisorClock) NewTimer(time.Duration) platformprocess.Timer { return newInstantTimer() }
func (clock *supervisorClock) advance(value time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(value)
	clock.mu.Unlock()
}

type recordingCommands struct {
	mu    sync.Mutex
	ports []uint16
}

func (commands *recordingCommands) BuildBundled(port uint16) (BundledCommand, error) {
	commands.mu.Lock()
	commands.ports = append(commands.ports, port)
	commands.mu.Unlock()
	return BundledCommand{Process: platformprocess.Command{Executable: "local-rag.exe", WorkingDirectory: "local-rag"}, Endpoint: fmt.Sprintf("http://127.0.0.1:%d", port)}, nil
}

func supervisorFixture(t *testing.T, processes ...*supervisorProcess) (*Supervisor, *recordingCommands, *supervisorClock, chan ProcessObservation) {
	t.Helper()
	clock := &supervisorClock{now: time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)}
	commands := &recordingCommands{}
	observations := make(chan ProcessObservation, 32)
	supervisor, err := NewSupervisor(SupervisorOptions{
		Adapter: &supervisorAdapter{processes: processes}, Job: &supervisorJob{}, Commands: commands,
		Readiness: ReadinessFunc(func(context.Context, string, platformprocess.LaunchGeneration) error { return nil }),
		Ports:     &portFake{ports: []uint16{9401, 9402, 9403}}, Clock: clock,
		Backoff: platformprocess.ExponentialBackoff{}, Observer: ProcessObserverFunc(func(value ProcessObservation) { observations <- value }),
		Policy: SupervisorPolicy{MaximumAttempts: 3, AttemptWindow: time.Minute, ReadinessTimeout: time.Second, StableReady: 10 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(supervisor.cancel)
	return supervisor, commands, clock, observations
}

func TestSupervisorPublishesReadyExitAndRestartsWithFreshGeneration(t *testing.T) {
	first, second := newSupervisorProcess(), newSupervisorProcess()
	supervisor, commands, _, observations := supervisorFixture(t, first, second)
	owned, endpoint, err := supervisor.StartBundled(t.Context(), 9400)
	if err != nil || owned != first || endpoint != "http://127.0.0.1:9400" || !first.assigned || !first.resumed {
		t.Fatalf("owned=%v endpoint=%s err=%v process=%#v", owned, endpoint, err, first)
	}
	first.exit(23)
	waitForSupervisorState(t, observations, StateReady, 2)
	if got := supervisor.Snapshot(); got.State != StateReady || got.Generation != 2 || got.Endpoint != "http://127.0.0.1:9401" {
		t.Fatalf("snapshot=%#v", got)
	}
	commands.mu.Lock()
	ports := append([]uint16(nil), commands.ports...)
	commands.mu.Unlock()
	if len(ports) != 2 || ports[0] != 9400 || ports[1] != 9401 || !first.closed.Load() {
		t.Fatalf("ports=%v first=%#v", ports, first)
	}
}

func TestSupervisorExhaustsBoundedReadinessAttemptsWithFreshPorts(t *testing.T) {
	first, second := newSupervisorProcess(), newSupervisorProcess()
	clock := &supervisorClock{now: time.Now()}
	commands := &recordingCommands{}
	supervisor, err := NewSupervisor(SupervisorOptions{
		Adapter: &supervisorAdapter{processes: []*supervisorProcess{first, second}}, Job: &supervisorJob{}, Commands: commands,
		Readiness: ReadinessFunc(func(context.Context, string, platformprocess.LaunchGeneration) error { return errors.New("not ready") }),
		Ports:     &portFake{ports: []uint16{9501}}, Clock: clock, Backoff: platformprocess.ExponentialBackoff{},
		Policy: SupervisorPolicy{MaximumAttempts: 2, AttemptWindow: time.Minute, ReadinessTimeout: time.Second, StableReady: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(supervisor.cancel)
	_, _, err = supervisor.StartBundled(t.Context(), 9500)
	if !errors.Is(err, ErrRestartExhausted) || supervisor.Snapshot().State != StateRestartExhausted || !first.terminated || !second.terminated {
		t.Fatalf("snapshot=%#v first=%#v second=%#v err=%v", supervisor.Snapshot(), first, second, err)
	}
	commands.mu.Lock()
	ports := append([]uint16(nil), commands.ports...)
	commands.mu.Unlock()
	if len(ports) != 2 || ports[0] != 9500 || ports[1] != 9501 {
		t.Fatalf("ports=%v", ports)
	}
}

func TestSupervisorStableReadyIntervalResetsAttemptWindow(t *testing.T) {
	first, second := newSupervisorProcess(), newSupervisorProcess()
	supervisor, _, clock, observations := supervisorFixture(t, first, second)
	if _, _, err := supervisor.StartBundled(t.Context(), 9600); err != nil {
		t.Fatal(err)
	}
	clock.advance(11 * time.Second)
	first.exit(9)
	waitForSupervisorState(t, observations, StateReady, 2)
	if got := supervisor.Snapshot(); got.Attempt != 1 || got.Generation != 2 {
		t.Fatalf("stable reset snapshot=%#v", got)
	}
}

func TestSupervisorGracefulStopWaitsThenClosesJob(t *testing.T) {
	process := newSupervisorProcess()
	process.stopExits = true
	job := &supervisorJob{}
	supervisor, err := NewSupervisor(SupervisorOptions{
		Adapter: &supervisorAdapter{processes: []*supervisorProcess{process}}, Job: job, Commands: &recordingCommands{},
		Readiness: ReadinessFunc(func(context.Context, string, platformprocess.LaunchGeneration) error { return nil }),
		Ports:     &portFake{ports: []uint16{9701}},
		Policy:    SupervisorPolicy{MaximumAttempts: 1, AttemptWindow: time.Minute, ReadinessTimeout: time.Second, StableReady: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = supervisor.StartBundled(t.Context(), 9700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err = supervisor.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if !process.stopRequested || process.terminated || !process.closed.Load() || !job.closed || supervisor.Snapshot().State != StateStopping {
		t.Fatalf("process=%#v job=%#v snapshot=%#v", process, job, supervisor.Snapshot())
	}
	if err = supervisor.Close(ctx); err != nil {
		t.Fatalf("repeated close err=%v", err)
	}
}

func TestSupervisorDeadlineFallsBackToJobAndExternalGetsNoStopSignal(t *testing.T) {
	process := newSupervisorProcess()
	job := &supervisorJob{}
	supervisor, err := NewSupervisor(SupervisorOptions{
		Adapter: &supervisorAdapter{processes: []*supervisorProcess{process}}, Job: job, Commands: &recordingCommands{},
		Readiness: ReadinessFunc(func(context.Context, string, platformprocess.LaunchGeneration) error { return nil }),
		Ports:     &portFake{ports: []uint16{9801}},
		Policy:    SupervisorPolicy{MaximumAttempts: 1, AttemptWindow: time.Minute, ReadinessTimeout: time.Second, StableReady: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = supervisor.StartBundled(t.Context(), 9800); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if err = supervisor.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if !process.stopRequested || !process.closed.Load() || !job.closed {
		t.Fatalf("fallback process=%#v job=%#v", process, job)
	}

	externalJob := &supervisorJob{}
	external, err := NewSupervisor(SupervisorOptions{
		Adapter: &supervisorAdapter{}, Job: externalJob, Commands: &recordingCommands{},
		Readiness: ReadinessFunc(func(context.Context, string, platformprocess.LaunchGeneration) error { return nil }), Ports: &portFake{},
		Policy: SupervisorPolicy{MaximumAttempts: 1, AttemptWindow: time.Minute, ReadinessTimeout: time.Second, StableReady: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	external.ObserveExternal("http://127.0.0.1:9900")
	if err = external.Stop(t.Context()); err != nil || !externalJob.closed || external.Snapshot().Ownership != platformprocess.OwnershipExternal {
		t.Fatalf("external snapshot=%#v job=%#v err=%v", external.Snapshot(), externalJob, err)
	}
}

func waitForSupervisorState(t *testing.T, observations <-chan ProcessObservation, state SupervisorState, generation platformprocess.LaunchGeneration) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	var previous uint64
	for {
		select {
		case observation := <-observations:
			if observation.Sequence <= previous {
				t.Fatalf("non-monotonic observations: %#v", observation)
			}
			previous = observation.Sequence
			if observation.State == state && observation.Generation == generation {
				return
			}
		case <-deadline:
			t.Fatalf("did not observe %s generation %d", state, generation)
		}
	}
}
