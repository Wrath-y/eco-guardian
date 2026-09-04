// Package bootstrap is the single process composition root. It wires concrete
// adapters to narrow application/runtime ports and contains no domain rules.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"sort"
	"sync"
	"time"

	ecoguardian "github.com/zouyi/eco-guardian"
	"github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/app"
	appruntime "github.com/zouyi/eco-guardian/internal/app/runtime"
	"github.com/zouyi/eco-guardian/internal/app/runtime/capability"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	runtimediagnostics "github.com/zouyi/eco-guardian/internal/app/runtime/diagnostics"
	runtimerecovery "github.com/zouyi/eco-guardian/internal/app/runtime/recovery"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	backupintegration "github.com/zouyi/eco-guardian/internal/backup/integration"
	"github.com/zouyi/eco-guardian/internal/backup/restorefs"
	"github.com/zouyi/eco-guardian/internal/backup/restorejournal"
	"github.com/zouyi/eco-guardian/internal/backup/rootconfig"
	"github.com/zouyi/eco-guardian/internal/buildinfo"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/httpapi"
	"github.com/zouyi/eco-guardian/internal/packageinfo"
	"github.com/zouyi/eco-guardian/internal/platform/appdir"
	"github.com/zouyi/eco-guardian/internal/platform/backuproot"
	platformbrowser "github.com/zouyi/eco-guardian/internal/platform/browser"
	"github.com/zouyi/eco-guardian/internal/platform/credential"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
)

const (
	defaultStartupTimeout  = 30 * time.Second
	defaultShutdownTimeout = 10 * time.Second
)

// Worker is the process-owned lifecycle boundary used by module workers and
// optional dependencies. Concrete business workers remain in their modules.
type Worker interface {
	Start(context.Context) error
	Close(context.Context) error
}

// ShutdownBoundary persists module-owned safe points after HTTP admission is
// closed and before workers or owned dependencies are stopped.
type ShutdownBoundary interface{ PrepareShutdown(context.Context) error }

// StatusObserver receives detached immutable status values. A slow observer
// is isolated by StatusStore's latest-only subscription.
type StatusObserver interface {
	Observe(appruntime.StatusSnapshot)
}

type BuildOptions struct {
	Args               []string
	Environment        runtimeconfig.Environment
	Paths              appdir.Paths
	Assets             fs.FS
	PackageRoot        string
	PackageVerifier    appruntime.PackageStartupPort
	Workers            []Worker
	Dependencies       []Worker
	ShutdownBoundaries []ShutdownBoundary
	ShutdownPolicies   []runtimerecovery.ShutdownPolicy
	Observers          []StatusObserver
	Browser            appruntime.BrowserStartupPort
	Stdout             io.Writer
	Diagnostics        io.Writer
}

// Process owns every process-level component and their shutdown order.
type Process struct {
	Coordinator *appruntime.Coordinator
	Status      *appruntime.StatusStore
	Host        *httpapi.Runtime
	Projects    *project.Manager
	Assets      fs.FS

	workers          *workerGroup
	dependencies     *workerGroup
	boundaries       []ShutdownBoundary
	recoveryShutdown *runtimerecovery.ShutdownCoordinator
	logger           *runtimediagnostics.Logger
	settings         *settingsStartup
	observerIDs      []uint64
	observerWait     sync.WaitGroup
	closeOnce        sync.Once
	closeErr         error
	shutdownSignal   <-chan struct{}
	stdout           io.Writer
	startupWindow    time.Duration
	shutdownWindow   time.Duration
}

type instanceShutdownController struct {
	once sync.Once
	done chan struct{}
}

func newInstanceShutdownController() *instanceShutdownController {
	return &instanceShutdownController{done: make(chan struct{})}
}

func (controller *instanceShutdownController) Request() {
	if controller != nil {
		controller.once.Do(func() { close(controller.done) })
	}
}

// Build resolves machine adapters and registers all currently applied core
// HTTP/application seams. Optional module workers are supplied through ports.
func Build(options BuildOptions) (*Process, error) {
	pathsProvided := options.Paths.Root != ""
	paths := options.Paths
	if paths.Root == "" {
		var err error
		paths, err = appdir.ResolveHost()
		if err != nil {
			return nil, err
		}
	}
	paths, err := paths.Ensure()
	if err != nil {
		return nil, err
	}
	logDirectory, err := resolveRuntimeLogDirectory(paths, pathsProvided)
	if err != nil {
		return nil, err
	}
	assets := options.Assets
	if assets == nil {
		assets = ecoguardian.Assets
	}
	host, err := httpapi.NewLoopbackRuntime(0)
	if err != nil {
		return nil, err
	}
	instanceSecret, err := project.NewInstanceSecret()
	if err != nil {
		return nil, err
	}
	instanceShutdown := newInstanceShutdownController()
	projectLocker := project.FileLocker{Owner: func() project.InstanceOwner {
		return project.InstanceOwner{Version: 1, PID: os.Getpid(), URL: host.URL(), Secret: instanceSecret}
	}}
	settingsStore := runtimeconfig.NewStore(paths.Settings)
	environment := options.Environment
	if environment == nil {
		environment = provider.OSEnvironment{}
	}
	settings := &settingsStartup{store: settingsStore, args: append([]string(nil), options.Args...), environment: environment, host: host}

	registry, err := domain.NewRegistry()
	if err != nil {
		return nil, fmt.Errorf("load domain registry: %w", err)
	}
	releaseCapabilities, err := newReleaseCapabilitySet()
	if err != nil {
		return nil, fmt.Errorf("load release capability registry: %w", err)
	}
	identity, err := buildinfo.Current()
	if err != nil {
		return nil, err
	}
	selectionTokens := project.NewTokenStore(5*time.Minute, nil)
	defaultBackupRoot := backuproot.DefaultRoot
	if pathsProvided {
		// Explicit path injection is used by isolated package/process tests and
		// embedders; keep all resulting files within that supplied root.
		defaultBackupRoot = func() (string, error) { return filepath.Join(paths.Root, "EcoGuardian Backups"), nil }
	}
	backupRoots := &rootconfig.Manager{Settings: settingsStore, Tokens: selectionTokens, DefaultRoot: defaultBackupRoot, Probe: backupfs.Probe{}}
	retentionPolicy := func() (int, int) {
		configured, _, loadErr := settingsStore.Load()
		if loadErr != nil {
			return 10, 5
		}
		return configured.Backup.DailyRetentionCount, configured.Backup.ReleaseMigrationRetention
	}
	backupServices := &backupRuntime{roots: backupRoots, appVersion: identity.Version, retention: retentionPolicy, selectionTokens: selectionTokens}
	journalStore, err := restorejournal.New(filepath.Join(paths.Runtime, "restore"))
	if err != nil {
		return nil, err
	}
	backupServices.journal = journalStore
	migrationBackup := backupintegration.MigrationBackup{Roots: backupRoots, AppVersion: identity.Version, Space: backupfs.Probe{}}
	recentProjects := project.NewFileRecentProjects(filepath.Dir(paths.Root))
	graphDescriptor := projector.Descriptor{SchemaVersion: projector.ProjectionSchemaV1, Version: projector.ProjectorV1, Relations: projector.V1Relations(), Formatter: projector.V1Formatter{}}
	projects := project.NewManager(
		selectionTokens,
		projectLocker,
		project.SQLiteFactory{
			Registry:                     registry,
			GraphVersionContributor:      projector.VersionContributor{Descriptor: graphDescriptor},
			SimulationVersionContributor: releaseCapabilities.simulation,
			RiskVersionContributor:       releaseCapabilities.risk,
			MigrationBackup:              migrationBackup,
			ConfigureBackup:              backupServices.Configure,
		},
		backupServices,
		recentProjects,
	)
	backupServices.manager = projects
	backupServices.ConfigureDetached(
		backupintegration.ManagedInventory{Roots: backupRoots, MaxSchema: store.DBSchemaVersion()},
		backupintegration.NewRecentProjectRegistry(recentProjects),
	)
	status := appruntime.NewStatusStore(nil)
	statusAssembler, err := appruntime.NewStatusAssembler(appruntime.StatusResourceInput{
		Lifecycle: status.Snapshot(), Build: identity, Project: appruntime.ProjectStatus{State: "none"},
		Capabilities: defaultRuntimeCapabilities(status.Snapshot()), LogLocation: runtimediagnostics.DisplayDirectory,
	})
	if err != nil {
		return nil, err
	}
	credentialResolver := provider.CredentialResolver{Store: credential.NewDefaultStore(), Environment: provider.OSEnvironment{}}
	aiCapabilities := &aiCapabilityRuntime{settings: settingsStore, credentials: credentialResolver}

	packageVerifier := options.PackageVerifier
	if packageVerifier == nil {
		packageVerifier = &embeddedPackageVerifier{assets: assets, packageRoot: options.PackageRoot, allowBundled: true}
	}
	graphDependency := &graphRuntimeDependency{settings: settings, root: paths.Root, invalidate: aiCapabilities.Invalidate}
	if source, ok := packageVerifier.(verifiedPackageSource); ok {
		graphDependency.packages = source
	}
	graphSyncWorker := &graphSyncRuntime{projects: projects, provider: graphDependency, descriptor: graphDescriptor}
	releaseWorker := &releaseRuntime{
		projects: projects, registry: releaseCapabilities.registry, graph: graphDependency, backups: backupServices,
		simulationVersion: releaseCapabilities.simulation.ImplementationVersion(),
	}
	versioningDependencies := app.VersioningDependencies{
		Catalog: releaseCapabilities.registry, Registry: releaseCapabilities.registry,
		Submit: releaseWorker.Submit, Cancel: releaseWorker.Cancel,
		GraphRuntime: app.GraphRuntimeStatusApplication{Provider: graphDependency},
	}
	versioningProvider := httpapi.VersioningServiceFromProjectManagerWithDependencies(projects, versioningDependencies)
	impactWorker := &impactRuntime{projects: projects, graph: graphDependency}
	workerValues := append([]Worker(nil), options.Workers...)
	workerValues = append(workerValues, graphSyncWorker, impactWorker, releaseWorker)
	workers := &workerGroup{values: workerValues}
	dependencyWorkers := make([]Worker, 0, len(options.Dependencies)+1)
	for _, dependency := range options.Dependencies {
		dependencyWorkers = append(dependencyWorkers, &packageGatedWorker{packages: packageVerifier, next: dependency})
	}
	dependencyWorkers = append(dependencyWorkers, graphDependency)
	dependencies := &workerGroup{values: dependencyWorkers}
	lifecycles := &dependencyLifecycles{dependencies: dependencies, workers: workers}
	browserPort := options.Browser
	if browserPort == nil {
		value := platformbrowser.NewDefault()
		browserPort = value
	}
	capabilityConvergence := runtimeCapabilityConvergence{
		graph: graphDependency, settings: settingsStore, credentials: credentialResolver, projects: projects, backups: backupServices,
		ai: aiCapabilities.Observe,
		release: func(ctx context.Context) (versioninggate.ReleaseCapability, error) {
			service := versioningProvider()
			if service == nil {
				return versioninggate.ReleaseCapability{Enabled: false, Reasons: []versioninggate.DisabledReason{{CapabilityID: "release", GateID: "project", Reason: "required gate is unregistered"}}}, nil
			}
			return service.ReleaseCapability(ctx)
		},
	}
	restoreRecovery := &backupintegration.RestoreStartupRecovery{
		Journal: journalStore, Locker: projectLocker, Registry: registry,
		MigrationBackup: migrationBackup, Replacement: restorefs.Replacement{Verifier: store.BackupVerifier{}}, Verifier: store.BackupVerifier{}, Recent: recentProjects,
	}
	recoveryCoordinator, err := runtimerecovery.NewCoordinator(runtimerecovery.StageDescriptor{
		Stage: runtimerecovery.StageRestoreCompatibility,
		Scanner: runtimerecovery.ScannerFunc(func(ctx context.Context) (runtimerecovery.ScanResult, error) {
			return runtimerecovery.ScanResult{}, restoreRecovery.Recover(ctx)
		}),
	})
	if err != nil {
		return nil, err
	}
	ports := appruntime.LifecyclePorts{
		Settings:     settings,
		Package:      packageVerifier,
		Host:         host,
		Dependencies: lifecycles,
		Recovery:     stagedStartupRecovery{coordinator: recoveryCoordinator},
		Projects:     recentProjectStartup{manager: projects, workers: workers},
		Capabilities: capabilityConvergence,
		Browser:      configuredBrowser{settings: settings, next: browserPort, diagnostics: options.Diagnostics},
	}
	coordinator, err := appruntime.NewCoordinator(status, ports)
	if err != nil {
		return nil, err
	}
	logPolicy := runtimeconfig.Default().Logs
	if configured, _, loadErr := settingsStore.Load(); loadErr == nil {
		logPolicy = configured.Logs
	}
	logger, loggerErr := runtimediagnostics.New(runtimediagnostics.Options{
		Directory: logDirectory, MaxBytes: logPolicy.MaxBytes, MaxFiles: logPolicy.MaxFiles, Capacity: 1024,
	})
	if loggerErr != nil && options.Diagnostics != nil {
		_, _ = fmt.Fprintln(options.Diagnostics, `{"event_name":"safe_error","component":"diagnostics","code":"LOG_UNAVAILABLE"}`)
	}
	if logger != nil {
		host.Engine().Use(logger.Middleware())
		settings.logger = logger
		graphDependency.logger = logger
		ports.Package = diagnosticPackageStartup{next: packageVerifier, logger: logger}
		coordinator, err = appruntime.NewCoordinator(status, ports)
		if err != nil {
			_ = logger.Close(context.Background())
			return nil, err
		}
	}
	statusObserver := runtimeStatusObserver{assembler: statusAssembler, build: identity, projects: projects, graph: graphDependency}
	graphDependency.refresh = runtimeCapabilityRefresher{status: status, convergence: capabilityConvergence, publish: statusObserver.Observe}.Refresh
	registerRoutes(host, projects, registry, settingsStore, credentialResolver, assets, statusAssembler, graphDependency, aiCapabilities.Observe, graphSyncWorker.Submit, impactWorker.Submit, backupServices, backupRoots, versioningDependencies, project.PeerInstanceClient{}, instanceSecret, instanceShutdown.Request)
	process := &Process{
		Coordinator: coordinator, Status: status, Host: host, Projects: projects, Assets: assets,
		workers: workers, dependencies: dependencies, boundaries: append([]ShutdownBoundary{backupServices}, options.ShutdownBoundaries...), settings: settings, stdout: options.Stdout,
		logger: logger, shutdownSignal: instanceShutdown.done,
		startupWindow: defaultStartupTimeout, shutdownWindow: defaultShutdownTimeout,
	}
	process.recoveryShutdown, err = runtimerecovery.NewShutdownCoordinator(options.ShutdownPolicies...)
	if err != nil {
		if logger != nil {
			_ = logger.Close(context.Background())
		}
		return nil, err
	}
	observers := []StatusObserver{statusObserver}
	if logger != nil {
		logger.Emit(runtimediagnostics.Event{Name: runtimediagnostics.EventPackageVerification, Component: "package", Phase: "initialized", Fields: map[string]any{
			"version": identity.Version, "build": identity.Build, "package_mode": identity.PackageMode,
		}})
		observers = append(observers, &runtimeLogObserver{logger: logger})
	}
	observers = append(observers, options.Observers...)
	process.startObservers(observers)
	return process, nil
}

type stagedStartupRecovery struct{ coordinator *runtimerecovery.Coordinator }

func (startup stagedStartupRecovery) Recover(ctx context.Context) error {
	if startup.coordinator == nil {
		return runtimerecovery.ErrRegistryInvalid
	}
	_, err := startup.coordinator.Recover(ctx)
	return err
}

func registerRoutes(host *httpapi.Runtime, projects *project.Manager, registry *domain.Registry, settings *runtimeconfig.Store, credentials provider.CredentialResolver, assets fs.FS, status *appruntime.StatusAssembler, runtimeActions httpapi.RuntimeActionService, aiCapabilities func(context.Context) provider.Capability, graphSubmit func(context.Context, graphsync.GraphJob) error, impactSubmit func(context.Context, domain.ID) error, backups *backupRuntime, backupRoots *rootconfig.Manager, versioningDependencies app.VersioningDependencies, other project.OtherInstanceCloser, instanceSecret string, shutdown func()) {
	engine := host.Engine()
	projectHandler := httpapi.NewProjectHandler(projects, project.NativeDirectorySelector{}, other)
	if dependency, ok := runtimeActions.(*graphRuntimeDependency); ok {
		projectHandler.ObserveProjectStateChanges(dependency.projectStateChanged)
	}
	projectHandler.Register(engine)
	httpapi.NewInstanceControlHandler(projects, instanceSecret, shutdown).Register(engine)
	httpapi.NewSchemaHandler(registry).Register(engine)
	httpapi.NewEntityHandler(httpapi.StoreFromProjectManager(projects)).Register(engine)
	httpapi.NewValidationHandler(httpapi.ValidationStoreFromProjectManager(projects)).Register(engine)
	httpapi.NewSettingsHandler(settings, func(name string) bool {
		secret, err := credentials.Resolve(context.Background(), name)
		return err == nil && secret.Present()
	}, httpapi.BackupRootSettings{Selection: backupRoots, Selector: project.NativeDirectorySelector{Title: "Select Eco Guardian backup folder"}, Projects: projects}).Register(engine)
	httpapi.NewCredentialHandler(credentials).Register(engine)

	graphProvider, _ := runtimeActions.(graphsync.GraphProvider)
	graph := httpapi.GraphSyncServiceFromProjectManagerWithProvider(projects, graphProvider, graphSubmit)
	versions := httpapi.NewVersionHandlerWithGraph(httpapi.VersioningServiceFromProjectManagerWithDependencies(projects, versioningDependencies), graph)
	versions.RegisterAICapabilityProvider(aiCapabilities)
	versions.RegisterBackupCapabilityProvider(backups.Capability)
	impactProvider, _ := runtimeActions.(httpapi.ImpactProvider)
	impactAnalyses := httpapi.ImpactAnalysisServiceFromProjectManager(projects, httpapi.ImpactServiceDependencies{Provider: impactProvider, Submit: impactSubmit})
	simulationJobs := httpapi.SimulationJobStoreFromProjectManager(projects)
	versions.RegisterDurableResolver(httpapi.ImpactJobResolver(impactAnalyses))
	versions.RegisterDurableResolver(httpapi.SimulationJobResolver(simulationJobs))
	versions.RegisterDurableResolver(httpapi.AIJobResolver(httpapi.AIJobRuntimeServiceFromProjectManager(projects)))
	backupProvider := backups.Provider(projects)
	restoreProvider := backups.RestoreProvider()
	versions.RegisterDurableResolver(httpapi.BackupJobResolver(backupProvider))
	versions.RegisterDurableResolver(httpapi.RestoreJobResolver(restoreProvider))
	versions.Register(engine)
	httpapi.NewBackupHandler(backupProvider, backups.retention, backups.DailyProvider()).Register(engine)
	httpapi.NewRestoreHandler(restoreProvider).Register(engine)
	httpapi.NewRuntimeStatusHandler(status).Register(engine)
	httpapi.NewRuntimeActionHandler(runtimeActions).Register(engine)
	httpapi.NewGraphHandler(graph).Register(engine)
	httpapi.NewImpactHandler(impactAnalyses).Register(engine)
	httpapi.NewSimulationAdmissionHandler(httpapi.SimulationAdmissionServiceFromProjectManager(projects), simulationJobs).Register(engine)
	httpapi.NewSimulationHandler(httpapi.SimulationRunStoreFromProjectManager(projects)).Register(engine)
	httpapi.NewAIDecisionHandler(httpapi.AIDecisionServiceFromProjectManager(projects)).Register(engine)
	httpapi.NewAIResourceHandler(httpapi.AIDesignJobServiceFromProjectManager(projects), httpapi.AIDraftPatchServiceFromProjectManager(projects)).Register(engine)
	httpapi.RegisterEmbeddedUI(engine, assets)
}

type runtimeStatusObserver struct {
	assembler *appruntime.StatusAssembler
	build     buildinfo.Info
	projects  *project.Manager
	graph     *graphRuntimeDependency
}

type runtimeLogObserver struct {
	logger       *runtimediagnostics.Logger
	listenerSeen bool
	capabilities map[string]capability.State
}

func (observer *runtimeLogObserver) Observe(snapshot appruntime.StatusSnapshot) {
	if observer == nil || observer.logger == nil {
		return
	}
	observer.logger.Emit(runtimediagnostics.Event{
		Name: runtimediagnostics.EventStartupPhase, Component: "runtime", Phase: string(snapshot.Phase),
		Correlation: runtimediagnostics.Correlation{LaunchGeneration: snapshot.Generation},
	})
	if snapshot.ListenerURL != "" && !observer.listenerSeen {
		observer.listenerSeen = true
		observer.logger.Emit(runtimediagnostics.Event{
			Name: runtimediagnostics.EventListenerSelected, Component: "http", State: "ready",
			Correlation: runtimediagnostics.Correlation{LaunchGeneration: snapshot.Generation},
			Fields:      map[string]any{"listener": snapshot.ListenerURL},
		})
	}
	for _, reason := range snapshot.Reasons {
		observer.logger.Emit(runtimediagnostics.Event{
			Name: runtimediagnostics.EventHealthTransition, Component: reason.Component, State: "degraded", Code: reason.Code,
			Correlation: runtimediagnostics.Correlation{LaunchGeneration: snapshot.Generation},
		})
	}
	if observer.capabilities == nil {
		observer.capabilities = map[string]capability.State{}
	}
	for _, result := range defaultRuntimeCapabilities(snapshot) {
		if previous, present := observer.capabilities[result.ID]; present && previous == result.State {
			continue
		}
		observer.capabilities[result.ID] = result.State
		observer.logger.Emit(runtimediagnostics.Event{
			Name: runtimediagnostics.EventCapabilityTransition, Component: result.ID, State: string(result.State),
			Correlation: runtimediagnostics.Correlation{LaunchGeneration: snapshot.Generation},
		})
	}
}

func (observer runtimeStatusObserver) Observe(snapshot appruntime.StatusSnapshot) {
	projectStatus := appruntime.ProjectStatus{State: "none"}
	if current, active := observer.projects.Current(); active {
		projectStatus.State = "active"
		projectStatus.ProjectID = string(current.ID)
	}
	if recent, err := observer.projects.Recent(); err == nil {
		projectStatus.RecentCount = len(recent)
		if projectStatus.RecentCount > 100 {
			projectStatus.RecentCount = 100
		}
	}
	for _, reason := range snapshot.Reasons {
		if reason.Code == "RECOVERY_REQUIRED" || reason.Code == "RECENT_PROJECT_RECOVERY_REQUIRED" {
			projectStatus.RecoveryRequired = true
			if projectStatus.State == "none" {
				projectStatus.State = "recovery_required"
			}
		}
	}
	dependencies := make([]appruntime.DependencyStatus, 0, len(snapshot.Observations))
	for _, value := range snapshot.Observations {
		state := value.State
		if state == string(capability.Available) {
			state = "healthy"
		}
		dependency := appruntime.DependencyStatus{ID: value.ID, State: state, Generation: value.Generation, ObservedAt: value.ObservedAt}
		for _, reason := range snapshot.Reasons {
			if reason.Component == value.ID {
				dependency.Reasons = append(dependency.Reasons, capability.Reason{Code: reason.Code, Component: reason.Component, ObservationGeneration: value.Generation})
			}
		}
		dependencies = append(dependencies, dependency)
	}
	processObservation, health := observer.graph.Snapshot()
	for index := range dependencies {
		if dependencies[index].ID == capability.ObservationGraphSync || dependencies[index].ID == capability.ObservationRetrieval {
			dependencies[index].ExpiresAt = health.ExpiresAt
		}
	}
	_, _ = observer.assembler.Publish(appruntime.StatusResourceInput{
		Lifecycle: snapshot, Build: observer.build, Project: projectStatus, Process: processObservation, Dependencies: dependencies,
		Capabilities: defaultRuntimeCapabilities(snapshot, observer.graph), Recovery: []appruntime.RecoveryStatus{}, LogLocation: runtimediagnostics.DisplayDirectory,
	})
}

// resolveRuntimeLogDirectory keeps the default runtime log beside the project
// sources. Explicit machine paths are used only by isolated Build callers such
// as tests and embedders so their files remain inside the supplied sandbox.
func resolveRuntimeLogDirectory(paths appdir.Paths, pathsProvided bool) (string, error) {
	if pathsProvided {
		return filepath.Join(paths.Root, "logs"), nil
	}
	projectDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve project log directory: %w", err)
	}
	projectDirectory, err = filepath.Abs(filepath.Clean(projectDirectory))
	if err != nil {
		return "", fmt.Errorf("canonicalize project log directory: %w", err)
	}
	return filepath.Join(projectDirectory, "logs"), nil
}

func defaultRuntimeCapabilities(snapshot appruntime.StatusSnapshot, graphs ...*graphRuntimeDependency) []capability.Result {
	observations := map[string]capability.Observation{}
	for _, value := range snapshot.Observations {
		state := capability.Unavailable
		switch capability.State(value.State) {
		case capability.Available:
			state = capability.Available
		case capability.Degraded:
			state = capability.Degraded
		}
		observation := capability.Observation{ID: value.ID, State: state, Generation: value.Generation, ObservedAt: value.ObservedAt}
		for _, reason := range snapshot.Reasons {
			if reason.Component == value.ID {
				observation.Reasons = append(observation.Reasons, capability.Reason{Code: reason.Code, Component: reason.Component, ObservationGeneration: value.Generation})
			}
		}
		observations[value.ID] = observation
	}
	results := capability.DefaultRegistry().Evaluate(observations)
	var graph *graphRuntimeDependency
	if len(graphs) > 0 {
		graph = graphs[0]
	}
	configured, observed := graphActionState(graph)
	actions := map[string]capability.Action{}
	for _, descriptor := range capability.DefaultActionRegistry().Descriptors() {
		actions[descriptor.ID] = descriptor.Action
	}
	aiUnavailable := observations[capability.ObservationAIProvider].State != capability.Available
	graphAffected := map[string]bool{
		capability.CapabilityGraphSync: true, capability.CapabilityDeterministicImpact: true,
		capability.CapabilityRetrieval: true, capability.CapabilityAIDesign: true, capability.CapabilityRelease: true,
	}
	for index := range results {
		if results[index].State == capability.Available {
			continue
		}
		if configured && graphAffected[results[index].ID] {
			results[index].Actions = append(results[index].Actions, actions[capability.ActionGraphReconnect])
			if observed {
				results[index].Actions = append(results[index].Actions, actions[capability.ActionRuntimeReprobe])
			}
		}
		if results[index].ID == capability.CapabilityAIDesign && aiUnavailable {
			results[index].Actions = append(results[index].Actions, actions[capability.ActionProviderSettings], actions[capability.ActionCredentialConfigure])
		}
		if results[index].ID == capability.CapabilityBackup {
			results[index].Actions = append(results[index].Actions, actions[capability.ActionBackupRetry], actions[capability.ActionBackupSettings])
		}
		sort.Slice(results[index].Actions, func(left, right int) bool { return results[index].Actions[left].ID < results[index].Actions[right].ID })
	}
	return results
}

func graphActionState(graph *graphRuntimeDependency) (configured, observed bool) {
	if graph == nil || graph.settings == nil || graph.settings.graphSettings().Mode == runtimeconfig.GraphDisabled {
		return false, false
	}
	_, health := graph.Snapshot()
	return true, health.Generation > 0
}

func (p *Process) startObservers(observers []StatusObserver) {
	for _, observer := range observers {
		if observer == nil {
			continue
		}
		id, updates := p.Status.Subscribe()
		p.observerIDs = append(p.observerIDs, id)
		p.observerWait.Add(1)
		go func(observer StatusObserver, updates <-chan appruntime.StatusSnapshot) {
			defer p.observerWait.Done()
			for snapshot := range updates {
				observer.Observe(snapshot)
			}
		}(observer, updates)
	}
}

func (p *Process) Start(ctx context.Context) (appruntime.StatusSnapshot, error) {
	if p == nil || p.Coordinator == nil {
		return appruntime.StatusSnapshot{}, errors.New("runtime process is unavailable")
	}
	snapshot, err := p.Coordinator.Start(ctx, p.startupWindow)
	if err == nil && p.stdout != nil && snapshot.ListenerURL != "" {
		_, _ = fmt.Fprintln(p.stdout, snapshot.ListenerURL)
	}
	return snapshot, err
}

// Close is idempotent and preserves the process ownership order: stop HTTP
// admission, stop workers/dependencies, close project stores, then publish the
// terminal status and release observers.
func (p *Process) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.closeOnce.Do(func() {
		shutdownContext := ctx
		if _, bounded := shutdownContext.Deadline(); !bounded {
			var cancel context.CancelFunc
			shutdownContext, cancel = context.WithTimeout(shutdownContext, p.shutdownWindow)
			defer cancel()
		}
		_, _ = p.Status.Transition(appruntime.PhaseStopping, nil)
		var errs []error
		if err := callBounded(shutdownContext, p.Host.StopHost); err != nil {
			errs = append(errs, err)
		}
		if p.recoveryShutdown != nil {
			if err := callBounded(shutdownContext, p.recoveryShutdown.Prepare); err != nil {
				errs = append(errs, err)
			}
		}
		for _, boundary := range p.boundaries {
			if boundary == nil {
				continue
			}
			if err := callBounded(shutdownContext, boundary.PrepareShutdown); err != nil {
				errs = append(errs, err)
			}
		}
		if err := callBounded(shutdownContext, p.workers.StopDependencies); err != nil {
			errs = append(errs, err)
		}
		if err := callBounded(shutdownContext, p.dependencies.StopDependencies); err != nil {
			errs = append(errs, err)
		}
		if err := callBounded(shutdownContext, p.Projects.Close); err != nil {
			errs = append(errs, err)
		}
		_, _ = p.Status.Transition(appruntime.PhaseStopped, nil)
		for _, id := range p.observerIDs {
			p.Status.Unsubscribe(id)
		}
		observersDone := make(chan struct{})
		go func() { p.observerWait.Wait(); close(observersDone) }()
		select {
		case <-observersDone:
		case <-shutdownContext.Done():
			errs = append(errs, shutdownContext.Err())
		}
		if p.logger != nil {
			p.logger.Emit(runtimediagnostics.Event{Name: runtimediagnostics.EventProcessLifecycle, Component: "runtime", Phase: "stopped"})
			if err := p.logger.Close(shutdownContext); err != nil {
				errs = append(errs, err)
			}
		}
		p.closeErr = errors.Join(errs...)
	})
	return p.closeErr
}

func callBounded(ctx context.Context, operation func(context.Context) error) error {
	if operation == nil {
		return nil
	}
	result := make(chan error, 1)
	go func() { result <- operation(ctx) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Process) Run(ctx context.Context) error {
	if _, err := p.Start(ctx); err != nil {
		return err
	}
	if p.shutdownSignal == nil {
		<-ctx.Done()
	} else {
		select {
		case <-ctx.Done():
		case <-p.shutdownSignal:
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), p.shutdownWindow)
	defer cancel()
	return p.Close(shutdown)
}

// Run builds the default process, or prints generated bounded launch help.
func Run(ctx context.Context, args []string, stdout io.Writer) error {
	for _, argument := range args {
		if argument == "--help" || argument == "-h" {
			_, err := fmt.Fprintln(stdout, runtimeconfig.Help())
			return err
		}
	}
	process, err := Build(BuildOptions{Args: args, Stdout: stdout, Diagnostics: stdout})
	if err != nil {
		return err
	}
	return process.Run(ctx)
}

type settingsStartup struct {
	mu          sync.RWMutex
	store       *runtimeconfig.Store
	args        []string
	environment runtimeconfig.Environment
	host        *httpapi.Runtime
	current     runtimeconfig.Settings
	logger      *runtimediagnostics.Logger
}

func (s *settingsStartup) LoadSettings(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	settings, migrated, err := s.store.Load()
	if err != nil {
		return err
	}
	if migrated {
		if err = s.store.Save(settings); err != nil {
			return err
		}
	}
	settings, launch, _, err := runtimeconfig.ApplyLaunchOverrides(settings, s.args, s.environment)
	if err != nil {
		return err
	}
	if err = s.host.SetPreferredPort(launch.PreferredPort); err != nil {
		return err
	}
	s.mu.Lock()
	s.current = settings
	s.mu.Unlock()
	if s.logger != nil {
		s.logger.Emit(runtimediagnostics.Event{Name: runtimediagnostics.EventConfigLoaded, Component: "settings", State: "loaded", Fields: map[string]any{"config_version": settings.SchemaVersion}})
	}
	return nil
}

func (s *settingsStartup) browserAutoOpen() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current.Browser.AutoOpen
}

func (s *settingsStartup) graphSettings() runtimeconfig.Graph {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current.Graph
}

type embeddedPackageVerifier struct {
	mu           sync.RWMutex
	assets       fs.FS
	packageRoot  string
	allowBundled bool
	verified     *packageinfo.VerifiedPackage
}

type diagnosticPackageStartup struct {
	next   appruntime.PackageStartupPort
	logger *runtimediagnostics.Logger
}

func (startup diagnosticPackageStartup) VerifyPackage(ctx context.Context) error {
	started := time.Now()
	startup.logger.Emit(runtimediagnostics.Event{Name: runtimediagnostics.EventPackageVerification, Component: "package", Phase: "started"})
	err := startup.next.VerifyPackage(ctx)
	state, code := "succeeded", ""
	if err != nil {
		state, code = "failed", "PACKAGE_VERIFICATION_FAILED"
	}
	startup.logger.Emit(runtimediagnostics.Event{Name: runtimediagnostics.EventPackageVerification, Component: "package", Phase: "completed", State: state, Code: code, DurationMS: time.Since(started).Milliseconds()})
	return err
}

func (v *embeddedPackageVerifier) VerifyPackage(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := buildinfo.Current()
	if err != nil {
		return err
	}
	expected, err := buildinfo.FileDigests(ecoguardian.Assets)
	if err != nil {
		return err
	}
	actual, err := buildinfo.FileDigests(v.assets)
	if err != nil {
		return err
	}
	for name, digest := range expected {
		if actual[name] != digest {
			return fmt.Errorf("embedded asset identity mismatch: %s", name)
		}
	}
	var mode packageinfo.Mode
	switch info.PackageMode {
	case buildinfo.PackageComplete:
		mode = packageinfo.ModeComplete
		v.setAllowBundled(false)
	case buildinfo.PackageLightweight:
		mode = packageinfo.ModeLightweight
		v.setAllowBundled(false)
	default:
		v.setVerified(nil)
		v.setAllowBundled(true)
		return nil
	}
	root := v.packageRoot
	if root == "" {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			return packageinfo.DiagnosticError{Code: packageinfo.CodeManifestMissing}
		}
		root = filepath.Dir(executable)
	}
	verified, err := packageinfo.LoadAndVerify(ctx, root, packageinfo.Expectations{
		PackageMode: mode, OperatingSystem: stdruntime.GOOS, Architecture: stdruntime.GOARCH,
		EcoGuardian: packageinfo.EcoIdentity{
			Version: info.Version, Build: info.Build, Commit: info.Commit,
			RuntimeStatusSchemaVersion: info.RuntimeStatusSchemaVersion, Executable: "eco-guardian.exe",
		},
		EmbeddedAssetDigests: info.EmbeddedAssetDigests,
	})
	if err != nil {
		v.setVerified(nil)
		return classifyPackageVerification(err)
	}
	v.setVerified(verified)
	v.setAllowBundled(mode == packageinfo.ModeComplete)
	return nil
}

func (v *embeddedPackageVerifier) AllowBundledExecution() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.allowBundled
}

func (v *embeddedPackageVerifier) setAllowBundled(value bool) {
	v.mu.Lock()
	v.allowBundled = value
	v.mu.Unlock()
}

func (v *embeddedPackageVerifier) VerifiedPackage() *packageinfo.VerifiedPackage {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.verified
}

func (v *embeddedPackageVerifier) setVerified(value *packageinfo.VerifiedPackage) {
	v.mu.Lock()
	v.verified = value
	v.mu.Unlock()
}

func classifyPackageVerification(err error) error {
	var failure packageinfo.DiagnosticError
	if !errors.As(err, &failure) || failure.Code != packageinfo.CodeComponentMissing && failure.Code != packageinfo.CodeComponentCorrupt || !failure.Component.Valid() {
		return err
	}
	component := map[packageinfo.ComponentKind]string{
		packageinfo.ComponentLocalRAG: "LOCAL_RAG", packageinfo.ComponentPythonRuntime: "PYTHON",
		packageinfo.ComponentEmbeddingModel: "EMBEDDING_MODEL", packageinfo.ComponentRerankModel: "RERANK_MODEL",
	}[failure.Component]
	condition := "MISSING"
	if failure.Code == packageinfo.CodeComponentCorrupt {
		condition = "CORRUPT"
	}
	reasons := []appruntime.RuntimeReason{{
		Code: "PACKAGE_" + component + "_" + condition, Component: string(failure.Component),
		Message: "A packaged optional component is unavailable",
	}}
	switch failure.Component {
	case packageinfo.ComponentLocalRAG, packageinfo.ComponentPythonRuntime:
		reasons = append(reasons,
			appruntime.RuntimeReason{Code: "BUNDLED_PROCESS_UNAVAILABLE", Component: "process", Message: "The bundled Graph process cannot start"},
			appruntime.RuntimeReason{Code: "GRAPH_CORE_UNAVAILABLE", Component: "graph", Message: "Graph core is unavailable"},
			appruntime.RuntimeReason{Code: "RETRIEVAL_UNAVAILABLE", Component: "retrieval", Message: "Graph retrieval is unavailable"},
		)
	case packageinfo.ComponentEmbeddingModel:
		reasons = append(reasons, appruntime.RuntimeReason{Code: "VECTOR_RETRIEVAL_UNAVAILABLE", Component: "retrieval", Message: "Vector retrieval is unavailable"})
	case packageinfo.ComponentRerankModel:
		reasons = append(reasons, appruntime.RuntimeReason{Code: "RERANK_RETRIEVAL_UNAVAILABLE", Component: "retrieval", Message: "Rerank retrieval is unavailable"})
	}
	return appruntime.StartupDegradations{Reasons: reasons}
}

type recentProjectStartup struct {
	manager *project.Manager
	workers *workerGroup
}

func (s recentProjectStartup) OpenRecentProject(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, active := s.manager.Current(); active {
		return s.workers.StartDependencies(ctx)
	}
	values, err := s.manager.Recent()
	if err != nil || len(values) == 0 {
		if err != nil {
			return err
		}
		return s.workers.StartDependencies(ctx)
	}
	_, err = s.manager.OpenRecent(ctx, values[0].ID)
	if err == nil {
		return s.workers.StartDependencies(ctx)
	}
	reason := appruntime.RuntimeReason{Component: "project", Message: "The recent project was not opened"}
	switch {
	case errors.Is(err, project.ErrProjectLocked):
		reason.Code = "RECENT_PROJECT_LOCKED"
	case errors.Is(err, store.ErrSchemaTooNew):
		reason.Code = "RECENT_PROJECT_SCHEMA_NEWER"
	case errors.Is(err, store.ErrMigrationBackupRequired), errors.Is(err, store.ErrProjectInvalid):
		reason.Code = "RECENT_PROJECT_RECOVERY_REQUIRED"
	default:
		reason.Code = "RECENT_PROJECT_UNAVAILABLE"
	}
	// Workers are process-owned and tolerate an absent project. Starting them
	// even when automatic reopen fails lets a later manual open use Graph,
	// impact, and release immediately without requiring an application restart.
	if workerErr := s.workers.StartDependencies(ctx); workerErr != nil {
		return workerErr
	}
	return appruntime.RecentProjectDegradation{Reason: reason}
}

type configuredBrowser struct {
	settings    *settingsStartup
	next        appruntime.BrowserStartupPort
	diagnostics io.Writer
}

func (b configuredBrowser) OpenBrowser(ctx context.Context, url string) error {
	if b.settings == nil || !b.settings.browserAutoOpen() || b.next == nil {
		return ctx.Err()
	}
	err := b.next.OpenBrowser(ctx, url)
	if err != nil && b.diagnostics != nil {
		_, _ = fmt.Fprintln(b.diagnostics, `{"event":"browser_launch_failed","code":"BROWSER_LAUNCH_FAILED"}`)
	}
	return err
}

type workerGroup struct {
	mu      sync.Mutex
	values  []Worker
	started int
}

type dependencyLifecycles struct {
	dependencies *workerGroup
	workers      *workerGroup
}

type bundledExecutionPolicy interface {
	AllowBundledExecution() bool
}

type packageGatedWorker struct {
	mu       sync.Mutex
	packages appruntime.PackageStartupPort
	next     Worker
	started  bool
}

func (worker *packageGatedWorker) Start(ctx context.Context) error {
	if worker == nil || worker.next == nil {
		return nil
	}
	if policy, ok := worker.packages.(bundledExecutionPolicy); ok && !policy.AllowBundledExecution() {
		return nil
	}
	if err := worker.next.Start(ctx); err != nil {
		return err
	}
	worker.mu.Lock()
	worker.started = true
	worker.mu.Unlock()
	return nil
}

func (worker *packageGatedWorker) Close(ctx context.Context) error {
	if worker == nil || worker.next == nil {
		return nil
	}
	worker.mu.Lock()
	started := worker.started
	worker.started = false
	worker.mu.Unlock()
	if !started {
		return nil
	}
	return worker.next.Close(ctx)
}

func (g *dependencyLifecycles) StartDependencies(ctx context.Context) error {
	return g.dependencies.StartDependencies(ctx)
}

func (g *dependencyLifecycles) StopDependencies(ctx context.Context) error {
	return errors.Join(g.workers.StopDependencies(ctx), g.dependencies.StopDependencies(ctx))
}

func (g *workerGroup) StartDependencies(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.started < len(g.values) {
		if err := g.values[g.started].Start(ctx); err != nil {
			for g.started > 0 {
				g.started--
				_ = g.values[g.started].Close(context.WithoutCancel(ctx))
			}
			return err
		}
		g.started++
	}
	return nil
}

func (g *workerGroup) StopDependencies(ctx context.Context) error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var errs []error
	for g.started > 0 {
		g.started--
		if err := g.values[g.started].Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
