package application

import (
	"context"
	"errors"
	"sync"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var (
	ErrRestoreUnavailable         = errors.New("restore is unavailable")
	ErrRestorePreflightStale      = errors.New("restore preflight is stale")
	ErrRestoreIncompatible        = errors.New("backup schema is not supported for restore")
	ErrRestoreIdentity            = errors.New("backup project identity does not match restore target")
	ErrRestoreRecoveryRequired    = errors.New("restore requires recovery before project use")
	ErrRestoreIdempotencyConflict = errors.New("restore idempotency key conflicts with a different request")
)

const RestoreJobKind sharedjob.Kind = "restore"

type RestoreService struct {
	Backups     *Service
	Inventory   ports.ManagedBackupInventory
	Artifacts   ports.BackupArtifactStore
	Verifier    ports.SnapshotVerifier
	Targets     ports.RestoreTargetResolver
	Space       ports.SpaceProbe
	Jobs        sharedjob.Store
	Events      sharedjob.EventStore
	Journal     ports.RestoreJournalStore
	Maintenance ports.MaintenanceLeaser
	Replacement ports.DatabaseReplacement
	Derived     ports.DerivedStateInvalidator
	Mandatory   ports.BackupInvoker
	Clock       ports.Clock
	Registry    ports.ProjectRegistry
	CommandGate func() error

	mu                 sync.Mutex
	preflights         map[string]backupdomain.RestorePreflight
	preflightTargets   map[string]ports.RestoreTargetState
	registryChallenges map[string]restoreRegistryChallenge
}

type restoreRegistryChallenge struct {
	BackupID domain.ID
	Target   ports.RestoreTargetState
}

func NewRestoreService(backups *Service, targets ports.RestoreTargetResolver, journal ports.RestoreJournalStore, maintenance ports.MaintenanceLeaser, replacement ports.DatabaseReplacement) *RestoreService {
	service := &RestoreService{Backups: backups, Targets: targets, Journal: journal, Maintenance: maintenance, Replacement: replacement, Clock: ClockFunc(time.Now), preflights: map[string]backupdomain.RestorePreflight{}, preflightTargets: map[string]ports.RestoreTargetState{}, registryChallenges: map[string]restoreRegistryChallenge{}}
	if backups != nil {
		service.Artifacts, service.Verifier, service.Space, service.Jobs, service.Events = backups.Artifacts, backups.Verifier, backups.Space, backups.Jobs, backups.Events
		service.Mandatory = MandatoryInvoker{Service: func() *Service { return backups }}
	}
	return service
}

func (service *RestoreService) Valid() bool {
	if service == nil || service.Verifier == nil || service.Targets == nil || service.Journal == nil || service.Maintenance == nil || service.Replacement == nil || service.Clock == nil {
		return false
	}
	active := service.Backups != nil && service.Artifacts != nil && service.Jobs != nil && service.Events != nil && service.Mandatory != nil
	return active || service.Inventory != nil
}

func (service *RestoreService) Preflight(ctx context.Context, backupID domain.ID, mode backupdomain.RestoreTargetMode) (backupdomain.RestorePreflight, error) {
	return service.PreflightTarget(ctx, backupID, mode, "", "")
}

func (service *RestoreService) PreflightTarget(ctx context.Context, backupID domain.ID, mode backupdomain.RestoreTargetMode, selectionToken, registryConfirmationToken string) (backupdomain.RestorePreflight, error) {
	if !service.Valid() || !backupID.Valid() || !mode.Valid() {
		return backupdomain.RestorePreflight{}, ErrRestoreUnavailable
	}
	if service.CommandGate != nil {
		if err := service.CommandGate(); err != nil {
			return backupdomain.RestorePreflight{}, err
		}
	}
	if mode == backupdomain.RestoreEmptySelection {
		return service.preflightEmpty(ctx, backupID, selectionToken, registryConfirmationToken)
	}
	if selectionToken != "" || registryConfirmationToken != "" || service.Backups == nil || service.Artifacts == nil {
		return backupdomain.RestorePreflight{}, ErrRestoreUnavailable
	}
	identity, err := service.Backups.Source.Identity(ctx)
	if err != nil {
		return backupdomain.RestorePreflight{}, err
	}
	record, err := service.inventoryRecord(ctx, identity.ProjectID, backupID)
	if err != nil {
		return backupdomain.RestorePreflight{}, err
	}
	if record.ProjectID != identity.ProjectID {
		return backupdomain.RestorePreflight{}, ErrRestoreIdentity
	}
	if record.Compatibility == backupdomain.CompatibilityNewer || record.Compatibility == backupdomain.CompatibilityUnknown {
		return backupdomain.RestorePreflight{}, ErrRestoreIncompatible
	}
	record, lease, err := service.Artifacts.Acquire(ctx, identity.ProjectID, backupID)
	if err != nil {
		return backupdomain.RestorePreflight{}, err
	}
	defer lease.Release()
	verified, err := service.Verifier.Verify(ctx, lease.DatabasePath(), identity.ProjectID, record.SchemaVersion)
	if err != nil || verified.Bytes != record.DBBytes || verified.SHA256 != record.DBSHA256 {
		return backupdomain.RestorePreflight{}, errors.Join(ErrRestorePreflightStale, err)
	}
	target, err := service.Targets.ResolveActive(ctx, identity.ProjectID)
	if err != nil || target.ProjectID != record.ProjectID {
		return backupdomain.RestorePreflight{}, errors.Join(ErrRestoreIdentity, err)
	}
	if service.Space != nil {
		if err = service.Space.RequireWritable(ctx, target.CanonicalPath); err != nil {
			return backupdomain.RestorePreflight{}, err
		}
		required := record.DBBytes
		if required <= (int64(^uint64(0)>>1))/3 {
			required *= 3
		}
		if err = service.Space.RequireAvailable(ctx, target.CanonicalPath, required); err != nil {
			return backupdomain.RestorePreflight{}, err
		}
	}
	now := service.Clock.Now().UTC()
	preflight, err := backupdomain.NewRestorePreflight(backupdomain.RestorePreflightBody{Version: backupdomain.RestorePreflightVersion, Backup: record, TargetMode: mode, TargetIdentity: target.Identity, TargetGeneration: target.Generation, Writable: true, FreeSpaceSufficient: true, MaintenanceAvailable: target.MaintenanceAvailable, RegistryState: target.RegistryState, Confirmation: backupdomain.RestoreConfirmation{RestorePreBackupRequired: true, MaintenanceRequired: true, MigrationRequired: record.SchemaVersion < identity.SchemaVersion, GraphPending: true}, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)})
	if err != nil {
		return backupdomain.RestorePreflight{}, err
	}
	service.mu.Lock()
	service.preflights[preflight.Generation] = preflight
	service.preflightTargets[preflight.Generation] = target
	service.mu.Unlock()
	return preflight, nil
}

func (service *RestoreService) preflightEmpty(ctx context.Context, backupID domain.ID, selectionToken, confirmationToken string) (backupdomain.RestorePreflight, error) {
	if service.Inventory == nil || service.Registry == nil || (selectionToken == "") == (confirmationToken == "") {
		return backupdomain.RestorePreflight{}, ErrRestoreUnavailable
	}
	record, lease, err := service.Inventory.Resolve(ctx, backupID)
	if err != nil {
		return backupdomain.RestorePreflight{}, err
	}
	defer lease.Release()
	if record.Compatibility == backupdomain.CompatibilityNewer || record.Compatibility == backupdomain.CompatibilityUnknown {
		return backupdomain.RestorePreflight{}, ErrRestoreIncompatible
	}
	verified, err := service.Verifier.Verify(ctx, lease.DatabasePath(), record.ProjectID, record.SchemaVersion)
	if err != nil || verified.Bytes != record.DBBytes || verified.SHA256 != record.DBSHA256 {
		return backupdomain.RestorePreflight{}, errors.Join(ErrRestorePreflightStale, err)
	}
	var target ports.RestoreTargetState
	registryState := "matched"
	issuedConfirmation := ""
	if confirmationToken != "" {
		service.mu.Lock()
		challenge, found := service.registryChallenges[confirmationToken]
		delete(service.registryChallenges, confirmationToken)
		service.mu.Unlock()
		if !found || challenge.BackupID != backupID || challenge.Target.ProjectID != record.ProjectID {
			return backupdomain.RestorePreflight{}, ErrRestorePreflightStale
		}
		target = challenge.Target
		confirmed, confirmErr := service.Registry.ConfirmMigration(ctx, record.ProjectID, confirmationToken, target.CanonicalPath)
		if confirmErr != nil || confirmed.CanonicalPath != target.CanonicalPath {
			return backupdomain.RestorePreflight{}, errors.Join(ErrRestorePreflightStale, confirmErr)
		}
		registryState = "confirmed"
	} else {
		target, err = service.Targets.ResolveEmpty(ctx, record.ProjectID, selectionToken)
		if err != nil || target.ProjectID != record.ProjectID {
			return backupdomain.RestorePreflight{}, errors.Join(ErrRestoreIdentity, err)
		}
		registered, found, resolveErr := service.Registry.Resolve(ctx, record.ProjectID)
		if resolveErr != nil {
			return backupdomain.RestorePreflight{}, resolveErr
		}
		if found && registered.CanonicalPath != target.CanonicalPath {
			issuedConfirmation, _, err = service.Registry.IssueMigrationConfirmation(ctx, record.ProjectID, registered.CanonicalPath, target.CanonicalPath)
			if err != nil {
				return backupdomain.RestorePreflight{}, err
			}
			registryState = "migration_confirmation_required"
			service.mu.Lock()
			service.registryChallenges[issuedConfirmation] = restoreRegistryChallenge{BackupID: backupID, Target: target}
			service.mu.Unlock()
		}
	}
	if err = service.requireRestoreSpace(ctx, target.CanonicalPath, record.DBBytes); err != nil {
		return backupdomain.RestorePreflight{}, err
	}
	now := service.Clock.Now().UTC()
	preflight, err := backupdomain.NewRestorePreflight(backupdomain.RestorePreflightBody{Version: backupdomain.RestorePreflightVersion, Backup: record, TargetMode: backupdomain.RestoreEmptySelection, TargetIdentity: target.Identity, TargetGeneration: target.Generation, Writable: true, FreeSpaceSufficient: true, MaintenanceAvailable: target.MaintenanceAvailable, RegistryState: registryState, RegistryConfirmationToken: issuedConfirmation, Confirmation: backupdomain.RestoreConfirmation{RestorePreBackupRequired: false, MaintenanceRequired: true, MigrationRequired: record.Compatibility == backupdomain.CompatibilityOlder, GraphPending: true}, CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute)})
	if err != nil {
		return backupdomain.RestorePreflight{}, err
	}
	service.mu.Lock()
	service.preflights[preflight.Generation] = preflight
	service.preflightTargets[preflight.Generation] = target
	service.mu.Unlock()
	return preflight, nil
}

func (service *RestoreService) Submit(ctx context.Context, generation string, backupID domain.ID, mode backupdomain.RestoreTargetMode, confirmation, idempotencyKey string) (sharedjob.Record, bool, error) {
	if !service.Valid() || idempotencyKey == "" || confirmation != "RESTORE" || !backupID.Valid() || !mode.Valid() {
		return sharedjob.Record{}, false, ErrRestoreUnavailable
	}
	if service.CommandGate != nil {
		if err := service.CommandGate(); err != nil {
			return sharedjob.Record{}, false, err
		}
	}
	service.mu.Lock()
	preflight, found := service.preflights[generation]
	target, targetFound := service.preflightTargets[generation]
	service.mu.Unlock()
	if !found || !targetFound || !preflight.Valid(service.Clock.Now().UTC()) || preflight.RegistryState == "migration_confirmation_required" {
		return sharedjob.Record{}, false, ErrRestorePreflightStale
	}
	if preflight.Backup.BackupID != backupID || preflight.TargetMode != mode {
		return sharedjob.Record{}, false, ErrRestorePreflightStale
	}
	expectedTarget := target
	target, err := service.Targets.Revalidate(ctx, expectedTarget)
	if err != nil {
		return sharedjob.Record{}, false, ErrRestorePreflightStale
	}
	_, artifactLease, err := service.revalidateArtifact(ctx, preflight.Backup)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	_ = artifactLease.Release()
	command := backupdomain.RestoreCommand{Version: backupdomain.RestoreCommandVersion, ProjectID: preflight.Backup.ProjectID, BackupID: preflight.Backup.BackupID, TargetMode: preflight.TargetMode, PreflightGeneration: generation, Confirmation: confirmation}
	hash, err := command.Hash()
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	request := sharedjob.Request{ProjectID: command.ProjectID, Kind: RestoreJobKind, InputHash: hash, IdempotencyKey: idempotencyKey, RequestHash: hash}
	var job sharedjob.Record
	var replay bool
	if mode == backupdomain.RestoreEmptySelection {
		job, replay, err = service.createDetachedJob(ctx, request)
	} else if service.Jobs != nil {
		job, replay, err = service.Jobs.CreateOrGet(ctx, request)
	} else {
		err = ErrRestoreUnavailable
	}
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	if _, journalFound, loadErr := service.Journal.Load(ctx, job.ID); loadErr != nil {
		return sharedjob.Record{}, false, loadErr
	} else if !journalFound {
		journal := ports.RestoreJournal{Version: backupdomain.RestoreJournalVersion, Job: job, Generation: 1, Phase: backupdomain.RestorePreflighted, ProjectID: job.ProjectID, BackupID: preflight.Backup.BackupID, ManifestHash: preflight.Backup.ManifestHash, DatabaseHash: preflight.Backup.DBSHA256, DatabaseBytes: preflight.Backup.DBBytes, SchemaVersion: preflight.Backup.SchemaVersion, CommandHash: hash, PreflightGeneration: generation, TargetMode: preflight.TargetMode, TargetPath: target.CanonicalPath, TargetIdentity: target.Identity, TargetGeneration: target.Generation}
		if _, err = service.Journal.CompareAndSwap(ctx, 0, journal); err != nil {
			return sharedjob.Record{}, false, err
		}
	}
	return job, replay, nil
}

func (service *RestoreService) createDetachedJob(ctx context.Context, request sharedjob.Request) (sharedjob.Record, bool, error) {
	if !request.Valid() {
		return sharedjob.Record{}, false, ErrRestoreUnavailable
	}
	journals, err := service.Journal.List(ctx)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	for _, journal := range journals {
		if journal.Job.ProjectID != request.ProjectID || journal.Job.IdempotencyKey != request.IdempotencyKey || journal.Job.Kind != request.Kind {
			continue
		}
		if !journal.Job.Request().Equivalent(request) {
			return sharedjob.Record{}, false, ErrRestoreIdempotencyConflict
		}
		return journal.Job, true, nil
	}
	id, err := domain.NewID()
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	now := service.Clock.Now().UTC()
	return sharedjob.Record{ID: id, ProjectID: request.ProjectID, Kind: request.Kind, RevisionID: request.RevisionID, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, Status: sharedjob.Queued, CreatedAt: now, UpdatedAt: now}, false, nil
}

func (service *RestoreService) Execute(ctx context.Context, jobID domain.ID) (_ sharedjob.Record, finalErr error) {
	if !service.Valid() || !jobID.Valid() {
		return sharedjob.Record{}, ErrRestoreUnavailable
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	journal, found, err := service.Journal.Load(ctx, jobID)
	if err != nil || !found {
		return sharedjob.Record{}, errors.Join(ErrRestoreUnavailable, err)
	}
	detached := journal.TargetMode == backupdomain.RestoreEmptySelection
	job := journal.Job
	if !detached {
		if service.Jobs == nil {
			return sharedjob.Record{}, ErrRestoreUnavailable
		}
		job, err = service.Jobs.GetJob(ctx, jobID)
	}
	if err != nil || job.Kind != RestoreJobKind || job.RequestHash != journal.CommandHash {
		return sharedjob.Record{}, errors.Join(ErrRestoreUnavailable, err)
	}
	if job.Status == sharedjob.Succeeded {
		return job, nil
	}
	if job.CancelGeneration > 0 {
		job, _, _ = service.transitionJob(ctx, &journal, detached, job.Status, sharedjob.Canceled, nil, job.CancelGeneration)
		journal.Job = job
		if terminalErr := service.terminalJournal(ctx, &journal, backupdomain.RestoreRolledBack); terminalErr == nil && !detached {
			_ = service.Journal.DeleteTerminal(context.WithoutCancel(ctx), job.ID, journal.Generation)
		}
		return job, context.Canceled
	}
	if job.Status == sharedjob.Queued {
		job, _, err = service.transitionJob(ctx, &journal, detached, sharedjob.Queued, sharedjob.Running, nil, 0)
		if err != nil {
			return sharedjob.Record{}, err
		}
	}
	journal.Job = job
	ordinal := journal.LastEventOrdinal
	appendEvent := func(events sharedjob.EventStore, phase string, progress int, warning, safeError string, result *sharedjob.Result) error {
		ordinal++
		event := sharedjob.Event{JobID: job.ID, Ordinal: ordinal, Phase: phase, Progress: progress, Warning: warning, SafeError: safeError, Result: result, CreatedAt: service.Clock.Now().UTC()}
		if detached && events == nil {
			journal.Events = append(journal.Events, event)
			return nil
		}
		_, _, eventErr := events.Append(ctx, event)
		return eventErr
	}
	if err = appendEvent(service.Events, "preflight_revalidated", 5, "", "", nil); err != nil {
		return sharedjob.Record{}, err
	}
	record, artifactLease, err := service.revalidateArtifact(ctx, backupdomain.InventoryRecord{BackupID: journal.BackupID, ProjectID: journal.ProjectID, ManifestHash: journal.ManifestHash, DBSHA256: journal.DatabaseHash})
	if err != nil {
		return service.failBeforeMaintenance(ctx, &journal, job, ordinal, err)
	}
	defer artifactLease.Release()
	expectedTarget := ports.RestoreTargetState{ProjectID: journal.ProjectID, Mode: journal.TargetMode, CanonicalPath: journal.TargetPath, Identity: journal.TargetIdentity, Generation: journal.TargetGeneration}
	if _, err = service.Targets.Revalidate(ctx, expectedTarget); err != nil {
		return service.failBeforeMaintenance(ctx, &journal, job, ordinal, ErrRestorePreflightStale)
	}
	if err = service.requireRestoreSpace(ctx, journal.TargetPath, record.DBBytes); err != nil {
		return service.failBeforeMaintenance(ctx, &journal, job, ordinal, err)
	}
	maintenance, err := service.Maintenance.Acquire(ctx, journal.ProjectID, journal.TargetIdentity)
	if err != nil {
		return service.failBeforeMaintenance(ctx, &journal, job, ordinal, err)
	}
	if err = maintenance.MarkNonInterruptible(); err != nil {
		_ = maintenance.Close(context.WithoutCancel(ctx))
		return service.failBeforeMaintenance(ctx, &journal, job, ordinal, err)
	}
	if err = appendEvent(service.Events, "maintenance", 15, "Cancellation is no longer available", "", nil); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, false, err)
	}
	if err = service.advanceJournal(ctx, &journal, backupdomain.RestoreMaintenance, ordinal); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, false, err)
	}
	if detached {
		journal.RestorePreNotApplicable = true
	} else {
		restorePre := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: journal.ProjectID, Purpose: backupdomain.RestorePre, CallerJobID: job.ID, CallerHash: job.RequestHash, Source: backupdomain.SourceIdentity{CallerJobID: job.ID, RequestHash: job.RequestHash}}
		restorePreResult, invokeErr := service.Mandatory.Invoke(ctx, restorePre)
		if invokeErr != nil {
			return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, false, invokeErr)
		}
		journal.RestorePreResult = &restorePreResult
	}
	restorePrePhase := "restore_pre_backup"
	if detached {
		restorePrePhase = "restore_pre_not_applicable"
	}
	if err = appendEvent(service.Events, restorePrePhase, 25, "", "", nil); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, false, err)
	}
	if err = service.advanceJournal(ctx, &journal, backupdomain.RestorePreBackup, ordinal); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, false, err)
	}
	if err = maintenance.CloseConnections(ctx); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, false, err)
	}
	if err = service.advanceJournal(ctx, &journal, backupdomain.RestoreConnectionsClosed, ordinal); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, true, err)
	}
	verifiedBeforeReplace, verifyErr := service.Verifier.Verify(ctx, artifactLease.DatabasePath(), journal.ProjectID, record.SchemaVersion)
	if verifyErr != nil || verifiedBeforeReplace.Bytes != record.DBBytes || verifiedBeforeReplace.SHA256 != record.DBSHA256 || maintenance.Path() != journal.TargetPath {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, true, errors.Join(ErrRestorePreflightStale, verifyErr))
	}
	if err = service.requireRestoreSpace(ctx, journal.TargetPath, record.DBBytes); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, true, err)
	}
	staged, err := service.Replacement.Stage(ctx, artifactLease.DatabasePath(), maintenance.Path(), job.ID, journal.ProjectID, record.SchemaVersion, record.DBBytes, record.DBSHA256)
	if err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, true, err)
	}
	journal.StagedPath, journal.StagedIdentity = staged.Path, staged.SHA256
	if err = service.advanceJournal(ctx, &journal, backupdomain.RestoreDatabaseStaged, ordinal); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, true, err)
	}
	var original ports.RestoreFileEvidence
	if detached {
		journal.OriginalNotApplicable = true
	} else {
		original, err = service.Replacement.ParkOriginal(ctx, maintenance.Path(), job.ID)
		if err != nil {
			return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, ports.RestoreFileEvidence{}, true, err)
		}
		journal.OriginalPath, journal.OriginalIdentity = original.Path, original.SHA256
	}
	if err = service.advanceJournal(ctx, &journal, backupdomain.RestoreOriginalParked, ordinal); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, true, err)
	}
	var installed ports.RestoreFileEvidence
	if detached {
		installed, err = service.Replacement.InstallEmpty(ctx, maintenance.Path(), job.ID, staged)
	} else {
		installed, err = service.Replacement.Install(ctx, maintenance.Path(), job.ID, staged)
	}
	if err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, true, err)
	}
	journal.InstalledPath, journal.InstalledIdentity = installed.Path, installed.SHA256
	if err = service.advanceJournal(ctx, &journal, backupdomain.RestoreInstalled, ordinal); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, true, err)
	}
	if _, err = service.Replacement.VerifyInstalled(ctx, maintenance.Path(), journal.ProjectID, record.SchemaVersion, record.DBBytes, record.DBSHA256); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, true, err)
	}
	if err = service.advanceJournal(ctx, &journal, backupdomain.RestoreVerified, ordinal); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, true, err)
	}
	reopened, err := maintenance.Reopen(ctx)
	if err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, true, err)
	}
	if err = reopened.VerifyRestoreRuntime(ctx); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, err)
	}
	if err = service.advanceJournal(ctx, &journal, backupdomain.RestoreReopened, ordinal); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, err)
	}
	if err = reopened.ReconcileRestoreJob(ctx, job, ordinal, journal.Generation); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, err)
	}
	if detached {
		for _, event := range journal.Events {
			if _, _, err = reopened.Append(ctx, event); err != nil {
				return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, err)
			}
		}
	} else if journal.RestorePreResult == nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, ErrRestoreRecoveryRequired)
	} else if err = reopened.ReconcileRestoreBackup(ctx, job.ID, *journal.RestorePreResult); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, errors.Join(ErrRestoreRecoveryRequired, err))
	}
	if err = appendEvent(reopened, "reopened", 90, "Graph verification is pending", "", nil); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, err)
	}
	var derived ports.DerivedStateInvalidator
	if service.Derived != nil {
		derived = service.Derived
	} else if reopenedDerived, ok := reopened.(ports.DerivedStateInvalidator); ok {
		derived = reopenedDerived
	}
	if derived != nil {
		if err = derived.InvalidateAfterRestore(ctx, journal.ProjectID, job.ID, job.RequestHash); err != nil {
			return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, err)
		}
		// Provider submission is intentionally asynchronous. Local admission
		// failures leave Graph pending and never roll back a verified DB restore.
		_ = derived.EnqueueFullRebuild(ctx, journal.ProjectID, job.ID, job.RequestHash)
	}
	if err = service.advanceJournal(ctx, &journal, backupdomain.RestoreReconciled, ordinal); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, err)
	}
	current, err := reopened.GetJob(ctx, job.ID)
	if err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, err)
	}
	result := &sharedjob.Result{Type: "restore", ID: job.ID, URL: "/api/v1/restores/" + string(job.ID)}
	if err = appendEvent(reopened, "succeeded", 100, "Graph verification is pending", "", result); err != nil {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, err)
	}
	job, changed, err := reopened.Transition(ctx, current.ID, current.Status, sharedjob.Succeeded, result, current.CancelGeneration)
	if err != nil || !changed {
		return service.recoverFailure(ctx, &journal, job, ordinal, maintenance, original, false, errors.Join(ErrRestoreRecoveryRequired, err))
	}
	journal.Job = job
	if err = service.advanceJournal(ctx, &journal, backupdomain.RestoreSucceeded, ordinal); err != nil {
		return job, err
	}
	if !detached {
		if err = service.Replacement.Cleanup(context.WithoutCancel(ctx), maintenance.Path(), job.ID, original); err != nil {
			return job, nil // installed result is durable; cleanup is retryable.
		}
	}
	if err = maintenance.Close(context.WithoutCancel(ctx)); err != nil {
		return job, err
	}
	_ = service.Journal.DeleteTerminal(context.WithoutCancel(ctx), job.ID, journal.Generation)
	return job, nil
}

func (service *RestoreService) requireRestoreSpace(ctx context.Context, targetPath string, databaseBytes int64) error {
	if service.Space == nil {
		return nil
	}
	if err := service.Space.RequireWritable(ctx, targetPath); err != nil {
		return err
	}
	required := databaseBytes
	if required > 0 && required <= (int64(^uint64(0)>>1))/3 {
		required *= 3
	}
	return service.Space.RequireAvailable(ctx, targetPath, required)
}

func (service *RestoreService) Cancel(ctx context.Context, jobID domain.ID) (sharedjob.Record, bool, error) {
	journal, found, err := service.Journal.Load(ctx, jobID)
	if err != nil || !found {
		return sharedjob.Record{}, false, err
	}
	if journal.Phase != backupdomain.RestorePreflighted {
		return journal.Job, true, nil
	}
	if journal.TargetMode == backupdomain.RestoreEmptySelection {
		now := service.Clock.Now().UTC()
		journal.Job.CancelGeneration++
		journal.Job.CancelRequestedAt = &now
		journal.Job.Status = sharedjob.Canceled
		journal.Job.UpdatedAt = now
		journal.LastEventOrdinal++
		journal.Events = append(journal.Events, sharedjob.Event{JobID: jobID, Ordinal: journal.LastEventOrdinal, Phase: "canceled", Progress: 100, CreatedAt: now})
		if err = service.terminalJournal(ctx, &journal, backupdomain.RestoreRolledBack); err != nil {
			return sharedjob.Record{}, false, err
		}
		return journal.Job, false, nil
	}
	if service.Jobs == nil {
		return sharedjob.Record{}, false, ErrRestoreUnavailable
	}
	return service.Jobs.RequestCancellation(ctx, jobID)
}

func (service *RestoreService) GetJob(ctx context.Context, jobID domain.ID) (sharedjob.Record, error) {
	journal, found, err := service.Journal.Load(ctx, jobID)
	if err == nil && found && journal.TargetMode == backupdomain.RestoreEmptySelection {
		return journal.Job, nil
	}
	if service.Jobs == nil {
		return sharedjob.Record{}, ErrRestoreUnavailable
	}
	return service.Jobs.GetJob(ctx, jobID)
}

func (service *RestoreService) ListEvents(ctx context.Context, jobID domain.ID, after int64) ([]sharedjob.Event, error) {
	journal, found, err := service.Journal.Load(ctx, jobID)
	if err == nil && found && journal.TargetMode == backupdomain.RestoreEmptySelection {
		result := make([]sharedjob.Event, 0, len(journal.Events))
		for _, event := range journal.Events {
			if event.Ordinal > after {
				result = append(result, event)
			}
		}
		return result, nil
	}
	if service.Events == nil {
		return nil, ErrRestoreUnavailable
	}
	return service.Events.ListEvents(ctx, jobID, after)
}

func (service *RestoreService) transitionJob(ctx context.Context, journal *ports.RestoreJournal, detached bool, expected, next sharedjob.Status, result *sharedjob.Result, observedCancel int64) (sharedjob.Record, bool, error) {
	if !detached {
		return service.Jobs.Transition(ctx, journal.Job.ID, expected, next, result, observedCancel)
	}
	current := journal.Job
	if current.Status != expected || current.CancelGeneration != observedCancel || !current.Status.CanTransitionTo(next) || next == sharedjob.Succeeded && (result == nil || !result.Valid()) {
		return current, false, ErrRestoreRecoveryRequired
	}
	current.Status, current.Result, current.UpdatedAt = next, result, service.Clock.Now().UTC()
	journal.Job = current
	return current, true, nil
}

func (service *RestoreService) revalidateArtifact(ctx context.Context, expected backupdomain.InventoryRecord) (backupdomain.InventoryRecord, ports.ArtifactLease, error) {
	if service.Artifacts == nil {
		if service.Inventory == nil {
			return backupdomain.InventoryRecord{}, nil, ErrRestoreUnavailable
		}
		record, lease, err := service.Inventory.Resolve(ctx, expected.BackupID)
		if err != nil || record.ProjectID != expected.ProjectID || expected.ManifestHash != "" && record.ManifestHash != expected.ManifestHash || expected.DBSHA256 != "" && record.DBSHA256 != expected.DBSHA256 {
			if lease != nil {
				_ = lease.Release()
			}
			return backupdomain.InventoryRecord{}, nil, errors.Join(ErrRestorePreflightStale, err)
		}
		verified, verifyErr := service.Verifier.Verify(ctx, lease.DatabasePath(), record.ProjectID, record.SchemaVersion)
		if verifyErr != nil || verified.Bytes != record.DBBytes || verified.SHA256 != record.DBSHA256 {
			_ = lease.Release()
			return backupdomain.InventoryRecord{}, nil, errors.Join(ErrRestorePreflightStale, verifyErr)
		}
		return record, lease, nil
	}
	record, err := service.inventoryRecord(ctx, expected.ProjectID, expected.BackupID)
	if err != nil || record.ManifestHash != expected.ManifestHash || record.DBSHA256 != expected.DBSHA256 {
		return backupdomain.InventoryRecord{}, nil, errors.Join(ErrRestorePreflightStale, err)
	}
	record, lease, err := service.Artifacts.Acquire(ctx, record.ProjectID, record.BackupID)
	if err != nil {
		return backupdomain.InventoryRecord{}, nil, err
	}
	verified, err := service.Verifier.Verify(ctx, lease.DatabasePath(), record.ProjectID, record.SchemaVersion)
	if err != nil || verified.Bytes != record.DBBytes || verified.SHA256 != record.DBSHA256 {
		_ = lease.Release()
		return backupdomain.InventoryRecord{}, nil, errors.Join(ErrRestorePreflightStale, err)
	}
	return record, lease, nil
}

func (service *RestoreService) inventoryRecord(ctx context.Context, projectID, backupID domain.ID) (backupdomain.InventoryRecord, error) {
	cursor := ""
	for {
		page, err := service.Artifacts.List(ctx, ports.InventoryQuery{ProjectID: projectID, After: cursor, Limit: 200})
		if err != nil {
			return backupdomain.InventoryRecord{}, err
		}
		for _, record := range page.Items {
			if record.BackupID == backupID {
				return record, nil
			}
		}
		if page.NextCursor == "" {
			return backupdomain.InventoryRecord{}, ErrRestorePreflightStale
		}
		cursor = page.NextCursor
	}
}

func (service *RestoreService) advanceJournal(ctx context.Context, journal *ports.RestoreJournal, phase backupdomain.RestorePhase, ordinal int64) error {
	next := *journal
	next.Generation++
	next.Phase = phase
	next.LastEventOrdinal = ordinal
	changed, err := service.Journal.CompareAndSwap(context.WithoutCancel(ctx), journal.Generation, next)
	if err != nil || !changed {
		return errors.Join(ErrRestoreRecoveryRequired, err)
	}
	*journal = next
	return nil
}

func (service *RestoreService) terminalJournal(ctx context.Context, journal *ports.RestoreJournal, phase backupdomain.RestorePhase) error {
	return service.advanceJournal(context.WithoutCancel(ctx), journal, phase, journal.LastEventOrdinal)
}

func (service *RestoreService) failBeforeMaintenance(ctx context.Context, journal *ports.RestoreJournal, job sharedjob.Record, ordinal int64, cause error) (sharedjob.Record, error) {
	if journal.TargetMode == backupdomain.RestoreEmptySelection {
		now := service.Clock.Now().UTC()
		ordinal++
		journal.Events = append(journal.Events, sharedjob.Event{JobID: job.ID, Ordinal: ordinal, Phase: "failed", Progress: 100, SafeError: "RESTORE_PREFLIGHT_FAILED", CreatedAt: now})
		job.Status, job.UpdatedAt = sharedjob.Failed, now
		journal.Job, journal.LastEventOrdinal = job, ordinal
		_ = service.terminalJournal(ctx, journal, backupdomain.RestoreRolledBack)
		return job, cause
	}
	current, _ := service.Jobs.GetJob(context.WithoutCancel(ctx), job.ID)
	_, _, _ = service.Events.Append(context.WithoutCancel(ctx), sharedjob.Event{JobID: job.ID, Ordinal: ordinal + 1, Phase: "failed", Progress: 100, SafeError: "RESTORE_PREFLIGHT_FAILED", CreatedAt: service.Clock.Now().UTC()})
	job, _, _ = service.Jobs.Transition(context.WithoutCancel(ctx), current.ID, current.Status, sharedjob.Failed, nil, current.CancelGeneration)
	journal.Job, journal.LastEventOrdinal = job, ordinal+1
	_ = service.terminalJournal(ctx, journal, backupdomain.RestoreRolledBack)
	_ = service.Journal.DeleteTerminal(context.WithoutCancel(ctx), job.ID, journal.Generation)
	return job, cause
}

func (service *RestoreService) recoverFailure(ctx context.Context, journal *ports.RestoreJournal, job sharedjob.Record, ordinal int64, maintenance ports.MaintenanceLease, original ports.RestoreFileEvidence, connectionsClosed bool, cause error) (sharedjob.Record, error) {
	recoveryContext := context.WithoutCancel(ctx)
	if journal.TargetMode == backupdomain.RestoreEmptySelection {
		if journal.Phase.AtOrAfter(backupdomain.RestoreInstalled) {
			journal.LastEventOrdinal = ordinal
			_ = service.terminalJournal(recoveryContext, journal, backupdomain.RestoreRecoveryRequired)
			return job, errors.Join(ErrRestoreRecoveryRequired, cause)
		}
		if journal.StagedPath != "" {
			staged := ports.RestoreFileEvidence{Path: journal.StagedPath, Bytes: journal.DatabaseBytes, SHA256: journal.StagedIdentity}
			if discardErr := service.Replacement.DiscardStaged(recoveryContext, maintenance.Path(), job.ID, staged); discardErr != nil {
				_ = service.terminalJournal(recoveryContext, journal, backupdomain.RestoreRecoveryRequired)
				return job, errors.Join(ErrRestoreRecoveryRequired, cause, discardErr)
			}
		}
		if closeErr := maintenance.Close(recoveryContext); closeErr != nil {
			_ = service.terminalJournal(recoveryContext, journal, backupdomain.RestoreRecoveryRequired)
			return job, errors.Join(ErrRestoreRecoveryRequired, cause, closeErr)
		}
		now := service.Clock.Now().UTC()
		ordinal++
		journal.Events = append(journal.Events, sharedjob.Event{JobID: job.ID, Ordinal: ordinal, Phase: "rolled_back", Progress: 100, SafeError: "RESTORE_ROLLED_BACK", CreatedAt: now})
		job.Status, job.UpdatedAt = sharedjob.Failed, now
		journal.Job, journal.LastEventOrdinal = job, ordinal
		_ = service.terminalJournal(recoveryContext, journal, backupdomain.RestoreRolledBack)
		return job, cause
	}
	if original.Path != "" && !connectionsClosed {
		journal.LastEventOrdinal = ordinal
		_ = service.terminalJournal(recoveryContext, journal, backupdomain.RestoreRecoveryRequired)
		return job, errors.Join(ErrRestoreRecoveryRequired, cause)
	}
	var reconciler ports.RestoreReconciliationStore
	var err error
	if original.Path != "" {
		err = service.Replacement.Rollback(recoveryContext, maintenance.Path(), job.ID, original)
	}
	if err == nil && connectionsClosed {
		reconciler, err = maintenance.Reopen(recoveryContext)
	}
	if err != nil {
		journal.LastEventOrdinal = ordinal
		_ = service.terminalJournal(recoveryContext, journal, backupdomain.RestoreRecoveryRequired)
		return job, errors.Join(ErrRestoreRecoveryRequired, cause, err)
	}
	jobs, events := service.Jobs, service.Events
	if reconciler != nil {
		if reconcileErr := reconciler.ReconcileRestoreJob(recoveryContext, job, ordinal, journal.Generation); reconcileErr != nil {
			_ = service.terminalJournal(recoveryContext, journal, backupdomain.RestoreRecoveryRequired)
			return job, errors.Join(ErrRestoreRecoveryRequired, cause, reconcileErr)
		}
		jobs, events = reconciler, reconciler
	}
	current, getErr := jobs.GetJob(recoveryContext, job.ID)
	if getErr == nil {
		ordinal++
		_, _, _ = events.Append(recoveryContext, sharedjob.Event{JobID: job.ID, Ordinal: ordinal, Phase: "rolled_back", Progress: 100, SafeError: "RESTORE_ROLLED_BACK", CreatedAt: service.Clock.Now().UTC()})
		job, _, _ = jobs.Transition(recoveryContext, current.ID, current.Status, sharedjob.Failed, nil, current.CancelGeneration)
	}
	journal.Job, journal.LastEventOrdinal = job, ordinal
	_ = service.terminalJournal(recoveryContext, journal, backupdomain.RestoreRolledBack)
	_ = maintenance.Close(recoveryContext)
	_ = service.Journal.DeleteTerminal(recoveryContext, job.ID, journal.Generation)
	return job, cause
}
