package application_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	backupintegration "github.com/zouyi/eco-guardian/internal/backup/integration"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/backup/restorefs"
	"github.com/zouyi/eco-guardian/internal/backup/restorejournal"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type recordingRestoreJournal struct {
	ports.RestoreJournalStore
	values []ports.RestoreJournal
}

func (journal *recordingRestoreJournal) CompareAndSwap(ctx context.Context, expected int64, next ports.RestoreJournal) (bool, error) {
	changed, err := journal.RestoreJournalStore.CompareAndSwap(ctx, expected, next)
	if changed {
		journal.values = append(journal.values, next)
	}
	return changed, err
}

func TestEmptyTargetRestoreUsesManagedInventoryExplicitRegistryMigrationAndNoRestorePreBackup(t *testing.T) {
	ctx := context.Background()
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	sourceDirectory := filepath.Clean(t.TempDir())
	source, _, err := store.Create(ctx, sourceDirectory, registry)
	if err != nil {
		t.Fatal(err)
	}
	projectID := source.ProjectID()
	managedRoot := filepath.Clean(t.TempDir())
	artifacts, err := backupfs.NewStore(managedRoot, store.DBSchemaVersion())
	if err != nil {
		t.Fatal(err)
	}
	backups := application.NewService(store.BackupSource{Store: source, AppVersion: "test"}, artifacts, store.BackupVerifier{}, source, source, backupfs.Probe{})
	command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: projectID, Purpose: backupdomain.Manual, ManualReason: "empty target restore", Source: backupdomain.SourceIdentity{}}
	backupJob, _, err := backups.Submit(ctx, command, "empty-target-source-backup")
	if err != nil {
		t.Fatal(err)
	}
	backupResult, err := backups.ExecuteStored(ctx, backupJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}

	recent := project.NewFileRecentProjects(t.TempDir())
	if err = recent.Record(project.ProjectInfo{ID: projectID, Name: "source", Path: sourceDirectory}); err != nil {
		t.Fatal(err)
	}
	tokens := project.NewTokenStore(time.Minute, nil)
	manager := project.NewManager(tokens, project.FileLocker{}, project.SQLiteFactory{Registry: registry}, project.NoJobs{}, recent)
	targets := backupintegration.NewProjectTargets(manager, tokens)
	journalStore, err := restorejournal.New(filepath.Join(filepath.Clean(t.TempDir()), "restore"))
	if err != nil {
		t.Fatal(err)
	}
	journal := &recordingRestoreJournal{RestoreJournalStore: journalStore}
	restores := application.NewRestoreService(nil, targets, journal, backupintegration.ProjectMaintenance{Manager: manager, Targets: targets}, restorefs.Replacement{Verifier: store.BackupVerifier{}})
	restores.Inventory = artifacts
	restores.Registry = backupintegration.NewRecentProjectRegistry(recent)
	restores.Verifier = store.BackupVerifier{}
	restores.Space = backupfs.Probe{}

	targetDirectory := filepath.Clean(t.TempDir())
	canonicalTarget, err := filepath.EvalSymlinks(targetDirectory)
	if err != nil {
		t.Fatal(err)
	}
	selectionToken, _, err := tokens.Issue(targetDirectory)
	if err != nil {
		t.Fatal(err)
	}
	required, err := restores.PreflightTarget(ctx, backupResult.BackupID, backupdomain.RestoreEmptySelection, selectionToken, "")
	if err != nil || required.RegistryState != "migration_confirmation_required" || required.RegistryConfirmationToken == "" || required.Confirmation.RestorePreBackupRequired {
		t.Fatalf("confirmation preflight=%#v err=%v", required, err)
	}
	values, err := recent.List()
	if err != nil || len(values) != 1 || values[0].Path != sourceDirectory {
		t.Fatalf("registry changed before confirmation: %#v err=%v", values, err)
	}
	confirmed, err := restores.PreflightTarget(ctx, backupResult.BackupID, backupdomain.RestoreEmptySelection, "", required.RegistryConfirmationToken)
	if err != nil || confirmed.RegistryState != "confirmed" || confirmed.RegistryConfirmationToken != "" {
		t.Fatalf("confirmed preflight=%#v err=%v", confirmed, err)
	}
	restoreJob, replay, err := restores.Submit(ctx, confirmed.Generation, backupResult.BackupID, backupdomain.RestoreEmptySelection, "RESTORE", "empty-target-restore")
	if err != nil || replay {
		t.Fatalf("submit job=%#v replay=%v err=%v", restoreJob, replay, err)
	}
	restoredJob, err := restores.Execute(ctx, restoreJob.ID)
	if err != nil || restoredJob.Status != "succeeded" {
		t.Fatalf("execute job=%#v err=%v journals=%#v", restoredJob, err, journal.values)
	}
	info, active := manager.Current()
	if !active || info.ID != projectID || info.Path != canonicalTarget {
		t.Fatalf("active project=%#v active=%v", info, active)
	}
	handle, available := manager.ActiveHandle()
	if !available {
		t.Fatal("restored project handle is not available")
	}
	reopened := handle.(interface{ Store() *store.Store }).Store()
	durable, err := reopened.GetJob(ctx, restoreJob.ID)
	if err != nil || durable.Status != "succeeded" || durable.RequestHash != restoreJob.RequestHash {
		t.Fatalf("durable restore job=%#v err=%v", durable, err)
	}
	events, err := reopened.ListEvents(ctx, restoreJob.ID, 0)
	if err != nil || len(events) < 4 || events[0].Phase != "preflight_revalidated" {
		t.Fatalf("durable restore events=%#v err=%v", events, err)
	}
	foundNotApplicable := false
	for _, value := range journal.values {
		if value.Phase == backupdomain.RestorePreBackup && value.RestorePreNotApplicable && value.RestorePreResult == nil {
			foundNotApplicable = true
		}
	}
	if !foundNotApplicable {
		t.Fatalf("restore-pre not-applicable checkpoint missing: %#v", journal.values)
	}
	values, err = recent.List()
	if err != nil || len(values) != 1 || values[0].ID != projectID || values[0].Path != canonicalTarget {
		t.Fatalf("registry was not committed after verified reopen: %#v err=%v", values, err)
	}
	if err = manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
