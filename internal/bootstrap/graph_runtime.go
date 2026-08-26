package bootstrap

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	aiopenai "github.com/zouyi/eco-guardian/internal/ai/provider/openai"
	appruntime "github.com/zouyi/eco-guardian/internal/app/runtime"
	"github.com/zouyi/eco-guardian/internal/app/runtime/capability"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	runtimediagnostics "github.com/zouyi/eco-guardian/internal/app/runtime/diagnostics"
	"github.com/zouyi/eco-guardian/internal/app/runtime/graphprocess"
	"github.com/zouyi/eco-guardian/internal/packageinfo"
	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
	"github.com/zouyi/eco-guardian/internal/project"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
)

type verifiedPackageSource interface {
	VerifiedPackage() *packageinfo.VerifiedPackage
	AllowBundledExecution() bool
}

type loopbackPortCandidates struct{}

func (loopbackPortCandidates) NextLoopbackPort(ctx context.Context) (uint16, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err = listener.Close(); err != nil {
		return 0, err
	}
	return uint16(port), nil
}

type graphRuntimeDependency struct {
	actionMu sync.Mutex
	mu       sync.Mutex
	settings *settingsStartup
	packages verifiedPackageSource
	root     string
	logger   *runtimediagnostics.Logger
	refresh  func(context.Context)

	refreshRequests chan struct{}
	refreshCancel   context.CancelFunc
	refreshWait     sync.WaitGroup

	supervisor *graphprocess.Supervisor
	output     *graphprocess.ChildOutputQueue
	observer   *graphprocess.HealthObserver
	endpoint   string
	process    graphprocess.ProcessObservation
	health     graphprocess.HealthObservation
}

func (dependency *graphRuntimeDependency) Start(ctx context.Context) error {
	if dependency == nil {
		return nil
	}
	dependency.actionMu.Lock()
	defer dependency.actionMu.Unlock()
	dependency.startRefreshLoop()
	return dependency.start(ctx)
}

func (dependency *graphRuntimeDependency) start(ctx context.Context) error {
	if dependency == nil || dependency.settings == nil {
		return nil
	}
	settings := dependency.settings.graphSettings()
	if settings.Mode == runtimeconfig.GraphDisabled {
		dependency.publishProcess(graphprocess.ProcessObservation{State: graphprocess.StateNotSelected, Ownership: platformprocess.OwnershipNone})
		dependency.setHealth(graphprocess.HealthObservation{Generation: 1, State: graphprocess.HealthUnavailable, Reason: "GRAPH_DISABLED", ObservedAt: time.Now().UTC()})
		return nil
	}
	client := &http.Client{Timeout: time.Duration(settings.HealthTimeoutSeconds) * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	healthAdapter := graphprocess.ClientHealthAdapter{HTTPClient: client}
	selector := graphprocess.Selector{Prober: healthAdapter, Ports: loopbackPortCandidates{}}
	allowBundled := settings.Mode == runtimeconfig.GraphBundled && dependency.packages != nil && dependency.packages.AllowBundledExecution() && dependency.packages.VerifiedPackage() != nil
	if allowBundled {
		if dependency.logger != nil {
			queue, err := graphprocess.NewChildOutputQueue(graphprocess.ChildOutputOptions{Capacity: 512, LineLimit: 8 << 10, Sink: runtimediagnostics.ChildSink{Logger: dependency.logger}})
			if err != nil {
				dependency.publishUnavailable("CHILD_LOG_UNAVAILABLE")
			} else {
				dependency.mu.Lock()
				dependency.output = queue
				dependency.mu.Unlock()
			}
		}
		supervisor, err := graphprocess.NewSupervisor(graphprocess.SupervisorOptions{
			Adapter: platformprocess.New(), Ports: loopbackPortCandidates{}, Readiness: healthAdapter,
			Commands: graphprocess.CommandFactoryFunc(func(port uint16) (graphprocess.BundledCommand, error) {
				return graphprocess.BuildBundledCommand(graphprocess.CommandRequest{
					Assets: dependency.packages.VerifiedPackage(), ApplicationDataRoot: dependency.root, Port: port, HostEnvironment: safeProcessEnvironment(),
				})
			}),
			Output: dependency.output, Observer: graphprocess.ProcessObserverFunc(dependency.publishProcess),
			Summaries: graphprocess.NewFileSummaryStore(filepath.Join(dependency.root, "runtime", "graph-process.json")),
			Policy: graphprocess.SupervisorPolicy{
				MaximumAttempts: max(settings.RestartLimit+1, 1), AttemptWindow: 5 * time.Minute,
				InitialBackoff: 250 * time.Millisecond, MaximumBackoff: 5 * time.Second,
				ReadinessTimeout: time.Duration(settings.StartupTimeoutSeconds) * time.Second, StableReady: 30 * time.Second,
			},
		})
		if err != nil {
			dependency.closeOutput(ctx)
			dependency.publishUnavailable("BUNDLED_PROCESS_UNAVAILABLE")
			return contextError(ctx)
		}
		dependency.mu.Lock()
		dependency.supervisor = supervisor
		dependency.mu.Unlock()
		selector.Starter = supervisor
	}
	selection, err := selector.Select(ctx, graphprocess.SelectionRequest{
		Mode: settings.Mode, ExplicitEndpoint: settings.Endpoint, PackageAllowsBundled: allowBundled, CandidateAttempts: max(settings.RestartLimit+1, 1),
	})
	if err != nil {
		dependency.publishUnavailable("GRAPH_SELECTION_UNAVAILABLE")
		dependency.mu.Lock()
		supervisor := dependency.supervisor
		dependency.supervisor = nil
		dependency.mu.Unlock()
		if supervisor != nil {
			_ = supervisor.Stop(context.WithoutCancel(ctx))
		}
		dependency.closeOutput(context.WithoutCancel(ctx))
		return contextError(ctx)
	}
	if selection.Ownership == platformprocess.OwnershipExternal {
		dependency.publishProcess(graphprocess.ProcessObservation{State: graphprocess.StateExternal, Ownership: selection.Ownership, Endpoint: selection.Endpoint})
	}
	observer, observerErr := graphprocess.NewHealthObserver(graphprocess.HealthObserverOptions{
		Source: healthAdapter, Timeout: time.Duration(settings.HealthTimeoutSeconds) * time.Second, TTL: 5 * time.Second,
	})
	if observerErr != nil {
		dependency.publishUnavailable("GRAPH_HEALTH_OBSERVER_UNAVAILABLE")
		return contextError(ctx)
	}
	health, _ := observer.Observe(ctx, selection.Endpoint)
	dependency.mu.Lock()
	dependency.observer = observer
	dependency.endpoint = selection.Endpoint
	dependency.health = health
	dependency.mu.Unlock()
	return nil
}

func (dependency *graphRuntimeDependency) publishUnavailable(reason string) {
	_, previous := dependency.Snapshot()
	dependency.setHealth(graphprocess.HealthObservation{
		Generation: previous.Generation + 1,
		State:      graphprocess.HealthUnavailable,
		Reason:     reason,
		ObservedAt: time.Now().UTC(),
	})
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (dependency *graphRuntimeDependency) Close(ctx context.Context) error {
	if dependency == nil {
		return nil
	}
	dependency.actionMu.Lock()
	defer dependency.actionMu.Unlock()
	err := dependency.close(ctx)
	dependency.stopRefreshLoop()
	return err
}

func (dependency *graphRuntimeDependency) close(ctx context.Context) error {
	dependency.mu.Lock()
	supervisor, output := dependency.supervisor, dependency.output
	dependency.supervisor, dependency.output, dependency.observer, dependency.endpoint = nil, nil, nil, ""
	dependency.mu.Unlock()
	var errs []error
	if supervisor != nil {
		errs = append(errs, supervisor.Stop(ctx))
	}
	if output != nil {
		errs = append(errs, output.Close(ctx))
	}
	return errors.Join(errs...)
}

func (dependency *graphRuntimeDependency) Reprobe(ctx context.Context) error {
	if dependency == nil {
		return graphprocess.ErrSelectionUnavailable
	}
	dependency.actionMu.Lock()
	dependency.mu.Lock()
	observer, endpoint := dependency.observer, dependency.endpoint
	dependency.mu.Unlock()
	if observer == nil || endpoint == "" {
		dependency.actionMu.Unlock()
		return graphprocess.ErrSelectionUnavailable
	}
	observer.Invalidate()
	health, err := observer.Observe(ctx, endpoint)
	if err == nil {
		dependency.setHealth(health)
	}
	dependency.actionMu.Unlock()
	dependency.triggerRefresh(ctx)
	return err
}

func (dependency *graphRuntimeDependency) Reconnect(ctx context.Context) error {
	if dependency == nil || dependency.settings == nil || dependency.settings.graphSettings().Mode == runtimeconfig.GraphDisabled {
		return graphprocess.ErrSelectionUnavailable
	}
	dependency.actionMu.Lock()
	closeErr := dependency.close(ctx)
	startErr := dependency.start(ctx)
	dependency.actionMu.Unlock()
	dependency.triggerRefresh(ctx)
	return errors.Join(closeErr, startErr)
}

func (dependency *graphRuntimeDependency) RuntimeActionPreconditions() map[string]bool {
	configured := dependency != nil && dependency.settings != nil && dependency.settings.graphSettings().Mode != runtimeconfig.GraphDisabled
	if dependency == nil {
		return map[string]bool{"dependency_observed": false, "graph_configured": configured}
	}
	dependency.mu.Lock()
	observed := dependency.observer != nil && dependency.endpoint != "" && dependency.health.Generation > 0
	dependency.mu.Unlock()
	return map[string]bool{"dependency_observed": observed, "graph_configured": configured}
}

func (dependency *graphRuntimeDependency) Snapshot() (graphprocess.ProcessObservation, graphprocess.HealthObservation) {
	if dependency == nil {
		return graphprocess.ProcessObservation{}, graphprocess.HealthObservation{}
	}
	dependency.mu.Lock()
	defer dependency.mu.Unlock()
	return dependency.process, dependency.health
}

func (dependency *graphRuntimeDependency) publishProcess(observation graphprocess.ProcessObservation) {
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = time.Now().UTC()
	}
	dependency.mu.Lock()
	dependency.process = observation
	logger := dependency.logger
	if observation.State == graphprocess.StateBackoff || observation.State == graphprocess.StateExited || observation.State == graphprocess.StateRestartExhausted {
		reason := observation.Reason
		if reason == "" {
			reason = "GRAPH_PROCESS_" + string(observation.State)
		}
		dependency.health = graphprocess.HealthObservation{
			Generation: dependency.health.Generation + 1,
			State:      graphprocess.HealthUnavailable,
			Reason:     reason,
			ObservedAt: observation.ObservedAt,
		}
		if dependency.observer != nil {
			dependency.observer.Invalidate()
		}
	}
	dependency.mu.Unlock()
	if logger != nil {
		logger.Emit(runtimediagnostics.Event{
			Name: runtimediagnostics.EventProcessLifecycle, Component: "local-rag", State: string(observation.State), Code: observation.Reason,
			Correlation: runtimediagnostics.Correlation{LaunchGeneration: uint64(observation.Generation)},
			Fields:      map[string]any{"ownership": string(observation.Ownership), "restart_attempt": observation.Attempt},
		})
	}
	dependency.requestRefresh()
}

func (dependency *graphRuntimeDependency) setHealth(observation graphprocess.HealthObservation) {
	dependency.mu.Lock()
	dependency.health = observation
	dependency.mu.Unlock()
}

func (dependency *graphRuntimeDependency) closeOutput(ctx context.Context) {
	dependency.mu.Lock()
	output := dependency.output
	dependency.output = nil
	dependency.mu.Unlock()
	if output != nil {
		_ = output.Close(ctx)
	}
}

func (dependency *graphRuntimeDependency) triggerRefresh(ctx context.Context) {
	dependency.mu.Lock()
	refresh := dependency.refresh
	dependency.mu.Unlock()
	if refresh != nil {
		refreshContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		refresh(refreshContext)
	}
}

func (dependency *graphRuntimeDependency) startRefreshLoop() {
	dependency.mu.Lock()
	if dependency.refresh == nil || dependency.refreshCancel != nil {
		dependency.mu.Unlock()
		return
	}
	refreshContext, cancel := context.WithCancel(context.Background())
	requests := make(chan struct{}, 1)
	dependency.refreshRequests = requests
	dependency.refreshCancel = cancel
	dependency.refreshWait.Add(1)
	dependency.mu.Unlock()
	go func() {
		defer dependency.refreshWait.Done()
		for {
			select {
			case <-requests:
				dependency.triggerRefresh(refreshContext)
			case <-refreshContext.Done():
				return
			}
		}
	}()
}

func (dependency *graphRuntimeDependency) requestRefresh() {
	if dependency == nil {
		return
	}
	dependency.mu.Lock()
	requests := dependency.refreshRequests
	dependency.mu.Unlock()
	if requests == nil {
		return
	}
	select {
	case requests <- struct{}{}:
	default:
	}
}

func (dependency *graphRuntimeDependency) stopRefreshLoop() {
	dependency.mu.Lock()
	cancel := dependency.refreshCancel
	dependency.refreshCancel = nil
	dependency.refreshRequests = nil
	dependency.mu.Unlock()
	if cancel != nil {
		cancel()
		dependency.refreshWait.Wait()
	}
}

func safeProcessEnvironment() map[string]string {
	result := map[string]string{}
	for _, name := range []string{"SYSTEMROOT", "WINDIR", "TEMP", "TMP"} {
		if value, present := os.LookupEnv(name); present {
			result[name] = value
		}
	}
	return result
}

type runtimeCapabilityConvergence struct {
	graph       *graphRuntimeDependency
	settings    *runtimeconfig.Store
	credentials aiprovider.CredentialResolver
	projects    *project.Manager
}

func (convergence runtimeCapabilityConvergence) ConvergeCapabilities(ctx context.Context) (appruntime.CapabilityConvergence, error) {
	now := time.Now().UTC()
	observations := map[string]capability.Observation{}
	for _, id := range []string{capability.ObservationLocalEditing, capability.ObservationValidation, capability.ObservationRevision, capability.ObservationSimulation, capability.ObservationBackup, capability.ObservationImpact} {
		observations[id] = capability.ModuleObservation(id, capability.Available, 1, now)
	}
	_, health := convergence.graph.Snapshot()
	if health.Generation == 0 {
		observations[capability.ObservationGraphSync] = capability.ModuleObservation(capability.ObservationGraphSync, capability.Unavailable, 1, now, "GRAPH_UNAVAILABLE")
		observations[capability.ObservationRetrieval] = capability.ModuleObservation(capability.ObservationRetrieval, capability.Unavailable, 1, now, "RETRIEVAL_UNAVAILABLE")
	} else {
		for _, observation := range capability.GraphObservations(health) {
			observations[observation.ID] = observation
		}
	}
	ai := appruntime.AICapabilityService{Settings: convergence.settings, Credentials: convergence.credentials, Prober: aiopenai.Prober{}}.Observe(ctx)
	observations[capability.ObservationAIProvider] = capability.AIObservation(ai, 1, now)
	release := versioninggate.ReleaseCapability{Enabled: false, Reasons: []versioninggate.DisabledReason{{CapabilityID: "release", GateID: "project", Reason: "gate registry unavailable"}}}
	if _, active := convergence.projects.Current(); active {
		// The project-owned release handler remains authoritative. Runtime status
		// stays unavailable until its exact candidate Gate result is observed.
		release.Reasons[0].Reason = "required gate is unregistered"
	}
	observations[capability.ObservationReleaseGates] = capability.ReleaseGateObservation(release, 1, now)
	results := capability.DefaultRegistry().Evaluate(observations)
	result := appruntime.CapabilityConvergence{Observations: make([]appruntime.RuntimeObservation, 0, len(observations))}
	for _, observation := range observations {
		result.Observations = append(result.Observations, appruntime.RuntimeObservation{ID: observation.ID, State: string(observation.State), Generation: observation.Generation, ObservedAt: observation.ObservedAt})
	}
	sort.Slice(result.Observations, func(left, right int) bool { return result.Observations[left].ID < result.Observations[right].ID })
	reasonSeen := map[string]struct{}{}
	for _, value := range results {
		if value.State == capability.Available {
			continue
		}
		result.Degraded = true
		for _, reason := range value.Reasons {
			key := reason.Code + "\x00" + reason.Component
			if _, exists := reasonSeen[key]; exists {
				continue
			}
			reasonSeen[key] = struct{}{}
			result.Reasons = append(result.Reasons, appruntime.RuntimeReason{Code: reason.Code, Component: reason.Component, Message: "Capability is not fully available"})
		}
	}
	return result, ctx.Err()
}

type runtimeCapabilityRefresher struct {
	status      *appruntime.StatusStore
	convergence runtimeCapabilityConvergence
	publish     func(appruntime.StatusSnapshot)
}

func (refresher runtimeCapabilityRefresher) Refresh(ctx context.Context) {
	if refresher.status == nil {
		return
	}
	current := refresher.status.Snapshot()
	if current.Phase != appruntime.PhaseReady && current.Phase != appruntime.PhaseDegraded {
		return
	}
	convergence, err := refresher.convergence.ConvergeCapabilities(ctx)
	reasons := preserveNonCapabilityReasons(current)
	if err != nil {
		reasons = append(reasons, appruntime.RuntimeReason{Code: "CAPABILITY_CONVERGENCE_FAILED", Component: "capabilities", Message: "Capability state could not fully converge"})
	} else {
		reasons = append(reasons, convergence.Reasons...)
	}
	next := appruntime.PhaseReady
	if err != nil || convergence.Degraded || len(reasons) > 0 {
		next = appruntime.PhaseDegraded
	}
	updated, transitionErr := refresher.status.Transition(next, func(snapshot *appruntime.StatusSnapshot) {
		snapshot.Reasons = append([]appruntime.RuntimeReason(nil), reasons...)
		if err == nil {
			snapshot.Observations = append([]appruntime.RuntimeObservation(nil), convergence.Observations...)
		}
	})
	if transitionErr == nil && refresher.publish != nil {
		refresher.publish(updated)
	}
}

func preserveNonCapabilityReasons(snapshot appruntime.StatusSnapshot) []appruntime.RuntimeReason {
	components := map[string]struct{}{"capabilities": {}}
	for _, observation := range snapshot.Observations {
		components[observation.ID] = struct{}{}
	}
	for _, result := range capability.DefaultRegistry().Evaluate(map[string]capability.Observation{}) {
		components[result.ID] = struct{}{}
	}
	result := make([]appruntime.RuntimeReason, 0, len(snapshot.Reasons))
	for _, reason := range snapshot.Reasons {
		if _, generated := components[reason.Component]; !generated {
			result = append(result, reason)
		}
	}
	return result
}
