// Package application orchestrates backup domain rules through ports.
package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var (
	ErrUnavailable       = errors.New("backup application is unavailable")
	ErrCommandMismatch   = errors.New("backup command does not match durable job")
	ErrPublicationFailed = errors.New("backup artifact publication failed")
)

const JobKind sharedjob.Kind = "backup"

type ClockFunc func() time.Time

func (function ClockFunc) Now() time.Time { return function() }

type IDFunc func() (domain.ID, error)

func (function IDFunc) New() (domain.ID, error) { return function() }

type RetentionPolicy struct {
	Daily            int
	ReleaseMigration int
}

type Service struct {
	Source    ports.ProjectSnapshotSource
	Inventory ports.ManagedBackupInventory
	Artifacts ports.BackupArtifactStore
	Verifier  ports.SnapshotVerifier
	Jobs      sharedjob.Store
	Events    sharedjob.EventStore
	Clock     ports.Clock
	IDs       ports.IDGenerator
	Space     ports.SpaceProbe
	Commands  ports.BackupCommandStore
	Audit     ports.BackupAuditStore
	Publish   ports.BackupPublicationStore
	Retention RetentionPolicy

	workerMu sync.Mutex
}

func NewInventoryService(inventory ports.ManagedBackupInventory) *Service {
	return &Service{Inventory: inventory, Clock: ClockFunc(time.Now), IDs: IDFunc(domain.NewID)}
}

func NewService(source ports.ProjectSnapshotSource, artifacts ports.BackupArtifactStore, verifier ports.SnapshotVerifier, jobs sharedjob.Store, events sharedjob.EventStore, space ports.SpaceProbe) *Service {
	service := &Service{Source: source, Artifacts: artifacts, Verifier: verifier, Jobs: jobs, Events: events, Clock: ClockFunc(time.Now), IDs: IDFunc(domain.NewID), Space: space, Retention: RetentionPolicy{Daily: 10, ReleaseMigration: 5}}
	service.Commands, _ = jobs.(ports.BackupCommandStore)
	service.Audit, _ = jobs.(ports.BackupAuditStore)
	service.Publish, _ = jobs.(ports.BackupPublicationStore)
	return service
}

func (service *Service) Valid() bool {
	return service.coreValid() && ((service.Jobs != nil && service.Events != nil) || service.Inventory != nil)
}

func (service *Service) coreValid() bool {
	return service != nil && ((service.Source != nil && service.Artifacts != nil && service.Verifier != nil) || service.Inventory != nil) && service.Clock != nil && service.IDs != nil
}

func (service *Service) ListInventory(ctx context.Context, after string, limit int) (ports.InventoryPage, error) {
	if service == nil || service.Inventory == nil {
		return ports.InventoryPage{}, ErrUnavailable
	}
	return service.Inventory.ListAll(ctx, after, limit)
}

func (service *Service) Submit(ctx context.Context, command backupdomain.Command, idempotencyKey string) (sharedjob.Record, bool, error) {
	if !service.Valid() || !command.Valid() || idempotencyKey == "" {
		return sharedjob.Record{}, false, ErrUnavailable
	}
	if service.Commands != nil {
		return service.Commands.AdmitBackup(ctx, command, idempotencyKey)
	}
	hash, err := command.Hash()
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	request := sharedjob.Request{ProjectID: command.ProjectID, Kind: JobKind, RevisionID: command.Source.RevisionID, InputHash: hash, IdempotencyKey: idempotencyKey, RequestHash: hash}
	return service.Jobs.CreateOrGet(ctx, request)
}

// ExecuteStored is the restart-safe worker entry point.
func (service *Service) ExecuteStored(ctx context.Context, jobID domain.ID) (backupdomain.Result, error) {
	if service == nil || service.Commands == nil {
		return backupdomain.Result{}, ErrUnavailable
	}
	command, err := service.Commands.GetBackupCommand(ctx, jobID)
	if err != nil {
		return backupdomain.Result{}, err
	}
	return service.Execute(ctx, jobID, command)
}

// Execute advances one admitted Job. The caller owns worker claiming; durable
// expected-state transitions prevent a second process from sealing success.
func (service *Service) Execute(ctx context.Context, jobID domain.ID, command backupdomain.Command) (backupdomain.Result, error) {
	if !service.Valid() || !jobID.Valid() || !command.Valid() {
		return backupdomain.Result{}, ErrUnavailable
	}
	service.workerMu.Lock()
	defer service.workerMu.Unlock()
	record, err := service.Jobs.GetJob(ctx, jobID)
	if err != nil {
		return backupdomain.Result{}, err
	}
	hash, err := command.Hash()
	if err != nil || record.Kind != JobKind || record.ProjectID != command.ProjectID || record.InputHash != hash || record.RequestHash != hash {
		return backupdomain.Result{}, ErrCommandMismatch
	}
	if record.Status == sharedjob.Succeeded && record.Result != nil {
		return service.findResult(ctx, record.ProjectID, record.Result.ID)
	}
	if record.Status == sharedjob.Canceled || record.Status == sharedjob.Failed {
		return backupdomain.Result{}, ErrPublicationFailed
	}
	if record.CancelGeneration > 0 {
		_, _, _ = service.Jobs.Transition(ctx, record.ID, record.Status, sharedjob.Canceled, nil, record.CancelGeneration)
		return backupdomain.Result{}, context.Canceled
	}
	if record.Status == sharedjob.Queued {
		record, _, err = service.Jobs.Transition(ctx, record.ID, sharedjob.Queued, sharedjob.Running, nil, record.CancelGeneration)
		if err != nil {
			return backupdomain.Result{}, err
		}
	}
	if record.Status != sharedjob.Running && record.Status != sharedjob.Interrupted {
		return backupdomain.Result{}, ErrCommandMismatch
	}
	ordinal, progressValue, err := service.eventPosition(ctx, record.ID)
	if err != nil {
		return backupdomain.Result{}, err
	}
	appendEvent := func(eventContext context.Context, phase string, progress int, warning, safeError string, result *sharedjob.Result) error {
		if progress < progressValue {
			progress = progressValue
		}
		ordinal++
		progressValue = progress
		_, _, eventErr := service.Events.Append(eventContext, sharedjob.Event{JobID: record.ID, Ordinal: ordinal, Phase: phase, Progress: progress, Warning: warning, SafeError: safeError, Result: result, CreatedAt: service.Clock.Now().UTC()})
		return eventErr
	}
	if err = appendEvent(ctx, "preparing", max(progressValue, 1), "", "", nil); err != nil {
		return backupdomain.Result{}, err
	}
	lastReported := progressValue
	artifactCommand := command
	if !command.Purpose.Mandatory() {
		artifactCommand.Source.CallerJobID = record.ID
		artifactCommand.Source.RequestHash = hash
	}
	result, recoveryErr := service.findPublished(ctx, artifactCommand)
	if recoveryErr != nil && !errors.Is(recoveryErr, ErrPublicationFailed) {
		return backupdomain.Result{}, recoveryErr
	}
	if errors.Is(recoveryErr, ErrPublicationFailed) {
		result, err = service.createArtifact(ctx, artifactCommand, func(progress ports.BackupProgress) error {
			current, getErr := service.Jobs.GetJob(ctx, record.ID)
			if getErr != nil {
				return getErr
			}
			if current.CancelGeneration != record.CancelGeneration || current.CancelGeneration > 0 {
				return context.Canceled
			}
			percent := 10
			if progress.TotalPages > 0 {
				percent = 10 + int(progress.CopiedPages*55/progress.TotalPages)
			}
			if percent > lastReported {
				lastReported = percent
				return appendEvent(ctx, "online_backup", percent, "", "", nil)
			}
			return nil
		}, func() error {
			current, getErr := service.Jobs.GetJob(ctx, record.ID)
			if getErr != nil || current.CancelGeneration != record.CancelGeneration || current.CancelGeneration > 0 {
				return context.Canceled
			}
			if service.Publish != nil {
				begun, beginErr := service.Publish.BeginBackupPublication(ctx, record.ID, record.CancelGeneration, service.Clock.Now().UTC())
				if beginErr != nil || !begun {
					return errors.Join(context.Canceled, beginErr)
				}
			}
			return appendEvent(ctx, "publishing", max(lastReported, 95), "", "", nil)
		})
	} else {
		err = nil
	}
	if err != nil {
		terminalContext := context.WithoutCancel(ctx)
		current, getErr := service.Jobs.GetJob(terminalContext, record.ID)
		if getErr == nil {
			next := sharedjob.Failed
			safeCode := "BACKUP_FAILED"
			if errors.Is(err, context.Canceled) || current.CancelGeneration > record.CancelGeneration {
				next, safeCode = sharedjob.Canceled, "BACKUP_CANCELED"
			}
			_ = appendEvent(terminalContext, "terminal", 100, "", safeCode, nil)
			_, _, _ = service.Jobs.Transition(terminalContext, record.ID, current.Status, next, nil, current.CancelGeneration)
		}
		return backupdomain.Result{}, err
	}
	if service.Audit != nil {
		if _, err = service.Audit.SealBackupResult(ctx, record.ID, result); err != nil {
			return backupdomain.Result{}, err
		}
	}
	if pruneErr := service.prune(ctx, command.ProjectID); pruneErr != nil {
		_ = appendEvent(context.WithoutCancel(ctx), "retention", max(lastReported, 99), "Backup succeeded; retention cleanup is pending", "", nil)
	}
	jobResult := &sharedjob.Result{Type: "backup", ID: result.BackupID, URL: result.ResultURL}
	if err = appendEvent(ctx, "published", 100, "", "", jobResult); err != nil {
		return backupdomain.Result{}, err
	}
	current, err := service.Jobs.GetJob(ctx, record.ID)
	if err != nil {
		return backupdomain.Result{}, err
	}
	if _, changed, transitionErr := service.Jobs.Transition(ctx, current.ID, current.Status, sharedjob.Succeeded, jobResult, current.CancelGeneration); transitionErr != nil || !changed {
		return backupdomain.Result{}, errors.Join(ErrPublicationFailed, transitionErr)
	}
	return result, nil
}

func (service *Service) ExecuteDirect(ctx context.Context, command backupdomain.Command, progress func(ports.BackupProgress) error) (backupdomain.Result, error) {
	if !service.coreValid() || !command.Valid() || !command.Purpose.Mandatory() {
		return backupdomain.Result{}, ErrUnavailable
	}
	service.workerMu.Lock()
	defer service.workerMu.Unlock()
	result, err := service.findPublished(ctx, command)
	if errors.Is(err, ErrPublicationFailed) {
		result, err = service.createArtifact(ctx, command, progress, nil)
	}
	if err != nil {
		return backupdomain.Result{}, err
	}
	if service.Audit != nil {
		if _, err = service.Audit.SealBackupResult(ctx, command.CallerJobID, result); err != nil {
			return backupdomain.Result{}, err
		}
	}
	_ = service.prune(ctx, command.ProjectID)
	return result, nil
}

func (service *Service) createArtifact(ctx context.Context, command backupdomain.Command, progress func(ports.BackupProgress) error, beforePublish func() error) (_ backupdomain.Result, finalErr error) {
	identity, err := service.Source.Identity(ctx)
	if err != nil || identity.ProjectID != command.ProjectID || identity.SchemaVersion < 1 {
		return backupdomain.Result{}, fmt.Errorf("%w: source_identity=%t schema_supported=%t: %v", ErrCommandMismatch, err == nil && identity.ProjectID == command.ProjectID, identity.SchemaVersion >= 1, err)
	}
	backupID, err := service.IDs.New()
	if err != nil || !backupID.Valid() {
		return backupdomain.Result{}, ErrUnavailable
	}
	artifact, err := service.Artifacts.CreateStaging(ctx, identity.ProjectID, backupID)
	if err != nil {
		return backupdomain.Result{}, err
	}
	published := false
	defer func() {
		closeErr := artifact.Close()
		if !published {
			discardErr := service.Artifacts.Discard(context.WithoutCancel(ctx), artifact)
			if closeErr != nil || discardErr != nil {
				finalErr = errors.Join(finalErr, closeErr, discardErr)
			}
		}
	}()
	if service.Space != nil {
		estimate := int64(1)
		if info, statErr := os.Stat(filepath.Join(identity.ProjectPath, "project.db")); statErr == nil && info.Size() > 0 {
			estimate = info.Size()
		}
		if err = service.Space.RequireWritable(ctx, filepath.Dir(artifact.DatabasePath())); err != nil {
			return backupdomain.Result{}, err
		}
		if err = service.Space.RequireAvailable(ctx, filepath.Dir(artifact.DatabasePath()), estimate); err != nil {
			return backupdomain.Result{}, err
		}
	}
	if err = service.Source.OnlineBackup(ctx, artifact.DatabasePath(), progress); err != nil {
		return backupdomain.Result{}, err
	}
	verified, err := service.Verifier.Verify(ctx, artifact.DatabasePath(), identity.ProjectID, identity.SchemaVersion)
	if err != nil {
		return backupdomain.Result{}, err
	}
	manifest, err := backupdomain.NewManifest(backupdomain.ManifestBody{ManifestVersion: backupdomain.ManifestSchemaVersion, BackupID: backupID, ProjectID: identity.ProjectID, Type: command.Purpose, Trigger: triggerForPurpose(command.Purpose), CreatedAt: service.Clock.Now().UTC(), AppVersion: identity.AppVersion, SchemaVersion: identity.SchemaVersion, DBBytes: verified.Bytes, DBSHA256: verified.SHA256, Source: command.Source, Extensions: map[string]json.RawMessage{}})
	if err != nil {
		return backupdomain.Result{}, err
	}
	encoded, err := manifest.CanonicalJSON()
	if err != nil {
		return backupdomain.Result{}, err
	}
	if err = artifact.WriteManifest(ctx, encoded); err != nil {
		return backupdomain.Result{}, err
	}
	if err = artifact.Flush(ctx); err != nil {
		return backupdomain.Result{}, err
	}
	verifiedAgain, err := service.Verifier.Verify(ctx, artifact.DatabasePath(), identity.ProjectID, identity.SchemaVersion)
	if err != nil || verifiedAgain.Bytes != verified.Bytes || verifiedAgain.SHA256 != verified.SHA256 {
		return backupdomain.Result{}, errors.Join(ErrPublicationFailed, err)
	}
	if progress != nil {
		if err = progress(ports.BackupProgress{CopiedPages: 1, TotalPages: 1, CopiedBytes: verified.Bytes}); err != nil {
			return backupdomain.Result{}, err
		}
	}
	if beforePublish != nil {
		if err = beforePublish(); err != nil {
			return backupdomain.Result{}, err
		}
	}
	result, err := service.Artifacts.Publish(ctx, artifact, manifest)
	if err != nil || !result.Valid() {
		return backupdomain.Result{}, errors.Join(ErrPublicationFailed, err)
	}
	published = true
	return result, nil
}

func (service *Service) eventPosition(ctx context.Context, jobID domain.ID) (int64, int, error) {
	events, err := service.Events.ListEvents(ctx, jobID, 0)
	if err != nil || len(events) == 0 {
		return 0, 0, err
	}
	last := events[len(events)-1]
	return last.Ordinal, last.Progress, nil
}

func (service *Service) findResult(ctx context.Context, projectID, backupID domain.ID) (backupdomain.Result, error) {
	cursor := ""
	for {
		page, err := service.Artifacts.List(ctx, ports.InventoryQuery{ProjectID: projectID, After: cursor, Limit: 200})
		if err != nil {
			return backupdomain.Result{}, err
		}
		for _, record := range page.Items {
			if record.BackupID == backupID && record.Restorable() {
				return backupdomain.Result{EvidenceVersion: backupdomain.EvidenceVersion, BackupID: record.BackupID, ProjectID: record.ProjectID, Type: record.Type, Trigger: triggerForPurpose(record.Type), CreatedAt: record.CreatedAt, AppVersion: record.AppVersion, SchemaVersion: record.SchemaVersion, DBBytes: record.DBBytes, DBSHA256: record.DBSHA256, ManifestHash: record.ManifestHash, Integrity: "ok", Source: record.Source, ResultURL: record.ResultURL}, nil
			}
		}
		if page.NextCursor == "" {
			return backupdomain.Result{}, ErrPublicationFailed
		}
		cursor = page.NextCursor
	}
}

func (service *Service) findPublished(ctx context.Context, command backupdomain.Command) (backupdomain.Result, error) {
	cursor := ""
	for {
		page, err := service.Artifacts.List(ctx, ports.InventoryQuery{ProjectID: command.ProjectID, After: cursor, Limit: 200})
		if err != nil {
			return backupdomain.Result{}, err
		}
		for _, record := range page.Items {
			if record.Type == command.Purpose && record.Source == command.Source && record.Restorable() {
				return backupdomain.Result{EvidenceVersion: backupdomain.EvidenceVersion, BackupID: record.BackupID, ProjectID: record.ProjectID, Type: record.Type, Trigger: triggerForPurpose(record.Type), CreatedAt: record.CreatedAt, AppVersion: record.AppVersion, SchemaVersion: record.SchemaVersion, DBBytes: record.DBBytes, DBSHA256: record.DBSHA256, ManifestHash: record.ManifestHash, Integrity: "ok", Source: record.Source, ResultURL: record.ResultURL}, nil
			}
		}
		if page.NextCursor == "" {
			return backupdomain.Result{}, ErrPublicationFailed
		}
		cursor = page.NextCursor
	}
}

func (service *Service) prune(ctx context.Context, projectID domain.ID) error {
	if service.Retention.Daily < 1 || service.Retention.ReleaseMigration < 1 {
		return nil
	}
	cursor := ""
	artifacts := []backupdomain.Artifact{}
	records := map[domain.ID]backupdomain.InventoryRecord{}
	for {
		page, err := service.Artifacts.List(ctx, ports.InventoryQuery{ProjectID: projectID, After: cursor, Limit: 200})
		if err != nil {
			return err
		}
		for _, record := range page.Items {
			records[record.BackupID] = record
			artifacts = append(artifacts, backupdomain.Artifact{ID: record.BackupID, Type: record.Type, CreatedAt: record.CreatedAt, Validation: record.Validation, Published: true})
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	selected, err := backupdomain.SelectRetention(artifacts, service.Retention.Daily, service.Retention.ReleaseMigration)
	if err != nil {
		return err
	}
	var cleanupErr error
	for _, id := range selected {
		record := records[id]
		if err = service.Artifacts.TrashAndDelete(ctx, projectID, id, record.ManifestHash); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
		}
	}
	return cleanupErr
}

func triggerForPurpose(value backupdomain.Type) backupdomain.Trigger {
	switch value {
	case backupdomain.Manual:
		return backupdomain.TriggerUser
	case backupdomain.Daily:
		return backupdomain.TriggerPolicy
	case backupdomain.Migration:
		return backupdomain.TriggerMigration
	case backupdomain.RestorePre:
		return backupdomain.TriggerRestore
	case backupdomain.Release:
		return backupdomain.TriggerRelease
	default:
		return ""
	}
}

func (service *Service) String() string { return fmt.Sprintf("backup service(kind=%s)", JobKind) }
