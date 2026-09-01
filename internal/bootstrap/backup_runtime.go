package bootstrap

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	backupintegration "github.com/zouyi/eco-guardian/internal/backup/integration"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/backup/restorefs"
	"github.com/zouyi/eco-guardian/internal/httpapi"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

// backupRuntime keeps exactly one application service per opened Store so
// HTTP requests, daily admission, and recovery workers share one execution
// lane. It contains no durable truth; all commands/results remain in the
// project Store and filesystem inventory.
type backupRuntime struct {
	roots            ports.ManagedRootSelection
	appVersion       string
	retention        func() (int, int)
	manager          *project.Manager
	journal          ports.RestoreJournalStore
	selectionTokens  project.TokenStore
	mu               sync.RWMutex
	services         map[*store.Store]*application.Service
	restores         map[*store.Store]*application.RestoreService
	currentRestore   *application.RestoreService
	detachedRestore  *application.RestoreService
	detachedBackup   *application.Service
	targets          *backupintegration.ProjectTargets
	registry         ports.ProjectRegistry
	inventory        ports.ManagedBackupInventory
	currentDaily     *application.DailyAdmission
	activeStore      *store.Store
	commandsDisabled bool
}

func (runtime *backupRuntime) PrepareShutdown(context.Context) error {
	if runtime == nil {
		return nil
	}
	runtime.mu.RLock()
	targets := runtime.targets
	runtime.mu.RUnlock()
	if targets == nil {
		return nil
	}
	return targets.Close()
}

func (runtime *backupRuntime) Provider(manager *project.Manager) httpapi.BackupServiceProvider {
	return func() *application.Service { return runtime.Current(manager) }
}

func (runtime *backupRuntime) RestoreProvider() httpapi.RestoreServiceProvider {
	return func() *application.RestoreService { return runtime.CurrentRestore() }
}

func (runtime *backupRuntime) DailyProvider() httpapi.DailyAdmissionProvider {
	return func() *application.DailyAdmission {
		runtime.mu.RLock()
		defer runtime.mu.RUnlock()
		return runtime.currentDaily
	}
}

func (runtime *backupRuntime) Configure(ctx context.Context, opened *store.Store) error {
	if runtime == nil || runtime.roots == nil || runtime.appVersion == "" || opened == nil {
		return application.ErrUnavailable
	}
	projectRoot, err := runtime.roots.ResolveProjectRoot(ctx, opened.ProjectID())
	if err != nil {
		// Preserve read-only project access while making the protected write
		// boundary fail closed until root settings are repaired.
		return opened.RegisterBusinessWriteAdmission(func(context.Context) error { return application.ErrUnavailable })
	}
	artifacts, err := backupfs.NewStore(filepath.Dir(projectRoot), store.DBSchemaVersion())
	if err != nil {
		return opened.RegisterBusinessWriteAdmission(func(context.Context) error { return application.ErrUnavailable })
	}
	service := application.NewService(store.BackupSource{Store: opened, AppVersion: runtime.appVersion}, artifacts, store.BackupVerifier{}, opened, opened, backupfs.Probe{})
	service.CommandGate = runtime.commandGate
	if runtime.retention != nil {
		service.Retention.Daily, service.Retention.ReleaseMigration = runtime.retention()
	}
	admission := &application.DailyAdmission{ProjectID: opened.ProjectID(), Backups: service, Jobs: opened, State: opened, Clock: service.Clock, Location: time.Local}
	if err = opened.RegisterBusinessWriteAdmission(admission.AdmitBusinessWrite); err != nil {
		return err
	}
	runtime.mu.Lock()
	if runtime.services == nil {
		runtime.services = map[*store.Store]*application.Service{}
	}
	runtime.services[opened] = service
	runtime.activeStore = opened
	runtime.currentDaily = admission
	if runtime.manager != nil && runtime.journal != nil {
		targets := runtime.targets
		if targets == nil {
			targets = backupintegration.NewProjectTargets(runtime.manager, runtime.selectionTokens)
			runtime.targets = targets
		}
		restoreService := application.NewRestoreService(service, targets, runtime.journal, backupintegration.ProjectMaintenance{Manager: runtime.manager, Targets: targets}, restorefs.Replacement{Verifier: store.BackupVerifier{}})
		restoreService.CommandGate = runtime.commandGate
		restoreService.Inventory, restoreService.Registry = runtime.inventory, runtime.registry
		if runtime.restores == nil {
			runtime.restores = map[*store.Store]*application.RestoreService{}
		}
		runtime.restores[opened] = restoreService
		runtime.currentRestore = restoreService
	}
	runtime.mu.Unlock()
	return nil
}

// Preflight is the existing ProjectCloseGuard adapter. Maintenance itself is
// guarded by ProjectManager; this check covers admitted backup/restore work
// before maintenance starts, including interrupted work awaiting recovery.
func (runtime *backupRuntime) Preflight(ctx context.Context) error {
	if runtime == nil {
		return project.ErrCloseBlocked
	}
	runtime.mu.RLock()
	opened := runtime.activeStore
	runtime.mu.RUnlock()
	if opened == nil {
		return nil
	}
	jobs, err := opened.ListRecoverableJobs(ctx, 1000)
	if err != nil {
		return err
	}
	coordinator, coordinated := project.MaintenanceCoordinator(ctx)
	for _, job := range jobs {
		if job.Kind == application.JobKind || job.Kind == application.RestoreJobKind {
			if coordinated && job.Kind == application.RestoreJobKind && job.ID == coordinator {
				continue
			}
			return project.ErrCloseBlocked
		}
	}
	return nil
}

func (runtime *backupRuntime) CurrentRestore() *application.RestoreService {
	if runtime == nil {
		return nil
	}
	active := false
	restoredTarget := false
	if runtime.manager != nil {
		_, active = runtime.manager.Current()
		restoredTarget = runtime.manager.RestoredTargetMaintenance()
	}
	runtime.mu.RLock()
	defer runtime.mu.RUnlock()
	if runtime.manager == nil || !active || restoredTarget {
		return runtime.detachedRestore
	}
	return runtime.currentRestore
}

func (runtime *backupRuntime) ConfigureDetached(inventory ports.ManagedBackupInventory, registry ports.ProjectRegistry) {
	if runtime == nil || runtime.manager == nil || runtime.journal == nil || inventory == nil || registry == nil {
		return
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.inventory, runtime.registry = inventory, registry
	runtime.detachedBackup = application.NewInventoryService(inventory)
	if runtime.targets == nil {
		runtime.targets = backupintegration.NewProjectTargets(runtime.manager, runtime.selectionTokens)
	}
	service := application.NewRestoreService(nil, runtime.targets, runtime.journal, backupintegration.ProjectMaintenance{Manager: runtime.manager, Targets: runtime.targets}, restorefs.Replacement{Verifier: store.BackupVerifier{}})
	service.CommandGate = runtime.commandGate
	service.Inventory, service.Registry, service.Verifier, service.Space = inventory, registry, store.BackupVerifier{}, backupfs.Probe{}
	runtime.detachedRestore = service
}

func (runtime *backupRuntime) commandGate() error {
	runtime.mu.RLock()
	disabled := runtime.commandsDisabled
	runtime.mu.RUnlock()
	if disabled {
		return application.ErrFeatureDisabled
	}
	return nil
}

// Rollback disables new backup/restore commands only after all durable
// restore journals and caller-owned backup/restore Jobs are terminal. It
// intentionally leaves artifacts, audit rows and journals untouched.
func (runtime *backupRuntime) Rollback(ctx context.Context) error {
	if runtime == nil {
		return application.ErrUnavailable
	}
	if runtime.journal != nil {
		journals, err := runtime.journal.List(ctx)
		if err != nil {
			return err
		}
		for _, journal := range journals {
			if !journal.Phase.Terminal() {
				return project.ErrCloseBlocked
			}
		}
	}
	runtime.mu.RLock()
	opened := runtime.activeStore
	runtime.mu.RUnlock()
	if opened != nil {
		jobs, err := opened.ListRecoverableJobs(ctx, 1000)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if job.Kind == application.JobKind || job.Kind == application.RestoreJobKind {
				return project.ErrCloseBlocked
			}
		}
	}
	runtime.mu.Lock()
	runtime.commandsDisabled = true
	runtime.mu.Unlock()
	return nil
}

func (runtime *backupRuntime) Current(manager *project.Manager) *application.Service {
	if runtime == nil || manager == nil {
		return nil
	}
	handle, ok := manager.ActiveHandle()
	if !ok {
		runtime.mu.RLock()
		defer runtime.mu.RUnlock()
		return runtime.detachedBackup
	}
	provider, ok := handle.(interface{ Store() *store.Store })
	if !ok {
		return nil
	}
	runtime.mu.RLock()
	service := runtime.services[provider.Store()]
	runtime.mu.RUnlock()
	return service
}

func (runtime *backupRuntime) Capability(ctx context.Context) httpapi.BackupRuntimeCapability {
	result := httpapi.BackupRuntimeCapability{RootHealth: "unknown", RestoreState: "idle", DisabledReasons: []string{}, SafeActions: []string{}}
	addReason := func(reason string) { result.DisabledReasons = append(result.DisabledReasons, reason) }
	if runtime == nil || runtime.manager == nil {
		addReason("BACKUP_IMPLEMENTATION_UNAVAILABLE")
		result.SafeActions = append(result.SafeActions, "retry", "settings")
		return result
	}
	info, active := runtime.manager.Current()
	if !active {
		addReason("BACKUP_PROJECT_UNAVAILABLE")
		result.SafeActions = append(result.SafeActions, "retry")
		return result
	}
	if maintenance, nonInterruptible := runtime.manager.MaintenanceState(); maintenance {
		result.RestoreState = "maintenance"
		if nonInterruptible {
			addReason("RESTORE_REPLACEMENT_NON_INTERRUPTIBLE")
		}
	}
	if runtime.Current(runtime.manager) == nil {
		addReason("BACKUP_SQLITE_UNAVAILABLE")
		result.SafeActions = append(result.SafeActions, "retry")
	}
	validator, ok := runtime.roots.(interface{ Validate(context.Context) error })
	if !ok {
		addReason("BACKUP_ROOT_UNAVAILABLE")
		result.SafeActions = append(result.SafeActions, "settings")
	} else if err := validator.Validate(ctx); err != nil {
		switch {
		case errors.Is(err, backupfs.ErrSpaceInsufficient):
			result.RootHealth = "insufficient_space"
			addReason("BACKUP_ROOT_INSUFFICIENT_SPACE")
		case errors.Is(err, backupfs.ErrRootUnwritable):
			result.RootHealth = "unwritable"
			addReason("BACKUP_ROOT_UNWRITABLE")
		default:
			result.RootHealth = "unavailable"
			addReason("BACKUP_ROOT_UNAVAILABLE")
		}
		result.SafeActions = append(result.SafeActions, "retry", "settings")
	} else {
		result.RootHealth = "healthy"
	}
	if runtime.journal != nil {
		journals, err := runtime.journal.List(ctx)
		if err != nil {
			addReason("RESTORE_RECOVERY_OBSERVATION_UNAVAILABLE")
			result.SafeActions = append(result.SafeActions, "inspect_recovery")
		} else {
			for _, journal := range journals {
				if journal.ProjectID != info.ID {
					continue
				}
				switch {
				case journal.Phase == backupdomain.RestoreRecoveryRequired:
					result.RestoreState, result.RecoveryRequired = "recovery_required", true
					addReason("RESTORE_RECOVERY_REQUIRED")
					result.SafeActions = append(result.SafeActions, "inspect_recovery")
				case !journal.Phase.Terminal() && result.RestoreState != "recovery_required":
					if journal.Phase.AtOrAfter(backupdomain.RestoreMaintenance) {
						result.RestoreState = "maintenance"
					} else {
						result.RestoreState = "preparing"
					}
				}
			}
		}
	}
	result.Available = len(result.DisabledReasons) == 0
	result.DisabledReasons = uniqueSorted(result.DisabledReasons)
	result.SafeActions = uniqueSorted(result.SafeActions)
	return result
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
