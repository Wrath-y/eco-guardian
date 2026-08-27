package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/backup/restorefs"
	"github.com/zouyi/eco-guardian/internal/backup/restorejournal"
	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type failingRestoreMigrationBackup struct{ calls int }

func (backup *failingRestoreMigrationBackup) Backup(context.Context, *store.Store, string, domain.ID) (store.BackupEvidence, error) {
	backup.calls++
	return store.BackupEvidence{}, errors.New("injected mandatory migration backup failure")
}

func TestRestoreStartupRecoveryCompletesProvenInstalledDatabase(t *testing.T) {
	ctx := context.Background()
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Clean(t.TempDir())
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	opened, projectID, err := store.Create(ctx, target, registry)
	if err != nil {
		t.Fatal(err)
	}
	_, revision, err := opened.Create(ctx, domain.KindTag, domain.EntityDraft{Key: "at_backup", Name: "At backup", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	if err = opened.CreateGraphSyncState(ctx, graphsync.SyncState{RevisionID: string(revision.ID), Pipeline: graphsync.StateReady, Warnings: []string{}}); err != nil {
		t.Fatal(err)
	}
	snapshotDirectory := filepath.Clean(t.TempDir())
	snapshotPath := filepath.Join(snapshotDirectory, "project.db")
	if err = (store.BackupSource{Store: opened, AppVersion: "test"}).OnlineBackup(ctx, snapshotPath, nil); err != nil {
		t.Fatal(err)
	}
	job := createRunningRestoreJob(t, ctx, opened, projectID)
	if err = opened.Close(); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []string{filepath.Join(target, "project.db-wal"), filepath.Join(target, "project.db-shm")} {
		if removeErr := os.Remove(sidecar); removeErr != nil && !os.IsNotExist(removeErr) {
			t.Fatal(removeErr)
		}
	}
	originalPath := filepath.Join(target, ".eco-restore-"+string(job.ID)+".original")
	if err = os.Rename(filepath.Join(target, "project.db"), originalPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(snapshotPath, filepath.Join(target, "project.db")); err != nil {
		t.Fatal(err)
	}
	originalHash, _ := testFileHash(t, originalPath)
	databaseHash, databaseBytes := testFileHash(t, filepath.Join(target, "project.db"))
	journalStore := newRecoveryJournalStore(t)
	journal := restoreJournalFixture(job, target, projectID, databaseHash, databaseBytes)
	journal.Phase = backupdomain.RestoreInstalled
	journal.StagedPath, journal.StagedIdentity = filepath.Join(target, ".eco-restore-"+string(job.ID)+".staging"), databaseHash
	journal.OriginalPath, journal.OriginalIdentity = originalPath, originalHash
	journal.InstalledPath, journal.InstalledIdentity = filepath.Join(target, "project.db"), databaseHash
	if changed, swapErr := journalStore.CompareAndSwap(ctx, 0, journal); swapErr != nil || !changed {
		t.Fatalf("create journal changed=%v err=%v", changed, swapErr)
	}
	recovery := &RestoreStartupRecovery{Journal: &deleteFailJournal{RestoreJournalStore: journalStore}, Locker: project.FileLocker{}, Registry: registry, Replacement: restorefs.Replacement{Verifier: store.BackupVerifier{}}, Verifier: store.BackupVerifier{}}
	if err = recovery.Recover(ctx); !errors.Is(err, errInjectedJournalDelete) {
		t.Fatalf("expected cleanup interruption, got %v", err)
	}
	if len(recovery.held) != 1 {
		t.Fatalf("held locks=%d", len(recovery.held))
	}
	if err = recovery.held[0].Release(); err != nil {
		t.Fatal(err)
	}
	leftover, found, err := journalStore.Load(ctx, job.ID)
	if err != nil || !found || leftover.Phase != backupdomain.RestoreSucceeded {
		t.Fatalf("leftover=%#v found=%v err=%v", leftover, found, err)
	}
	recovery = &RestoreStartupRecovery{Journal: journalStore, Locker: project.FileLocker{}, Registry: registry, Replacement: restorefs.Replacement{Verifier: store.BackupVerifier{}}, Verifier: store.BackupVerifier{}}
	if err = recovery.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(originalPath); !os.IsNotExist(err) {
		t.Fatalf("original was not cleaned: %v", err)
	}
	restored, _, err := store.OpenWithMigrationBackup(ctx, target, registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	durable, err := restored.GetJob(ctx, job.ID)
	if err != nil || durable.Status != sharedjob.Succeeded || durable.Result == nil {
		t.Fatalf("durable=%#v err=%v", durable, err)
	}
	if journal.RestorePreResult == nil {
		t.Fatal("restore-pre evidence was not journaled")
	}
	reconciledBackup, err := restored.GetBackupResult(ctx, journal.RestorePreResult.BackupID)
	if err != nil || reconciledBackup.Source.CallerJobID != job.ID {
		t.Fatalf("reconciled restore-pre=%#v err=%v", reconciledBackup, err)
	}
	if _, found, loadErr := journalStore.Load(ctx, job.ID); loadErr != nil || found {
		t.Fatalf("terminal journal found=%v err=%v", found, loadErr)
	}
	graphState, found, err := restored.GetGraphSyncState(ctx, revision.ID)
	if err != nil || !found || graphState.Pipeline != graphsync.StateSaved || graphState.Generation != 1 {
		t.Fatalf("graph state=%#v found=%v err=%v", graphState, found, err)
	}
}

func TestRestoreStartupRecoveryCompletesInstalledEmptyTargetWithoutOriginalOrRestorePre(t *testing.T) {
	ctx := context.Background()
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	sourceDirectory := filepath.Clean(t.TempDir())
	source, projectID, err := store.Create(ctx, sourceDirectory, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = source.Create(ctx, domain.KindTag, domain.EntityDraft{Key: "startup_empty", Name: "Startup empty", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}}); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(t.TempDir(), "project.db")
	if err = (store.BackupSource{Store: source, AppVersion: "test"}).OnlineBackup(ctx, snapshotPath, nil); err != nil {
		t.Fatalf("online backup: %+v", err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Clean(t.TempDir())
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	sourceFile, err := os.Open(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	destination, err := os.OpenFile(filepath.Join(target, "project.db"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = io.Copy(destination, sourceFile); err != nil {
		t.Fatal(err)
	}
	_ = sourceFile.Close()
	if err = destination.Close(); err != nil {
		t.Fatal(err)
	}
	databaseHash, databaseBytes := testFileHash(t, filepath.Join(target, "project.db"))
	now := time.Now().UTC()
	jobID := mustRecoveryID()
	job := sharedjob.Record{ID: jobID, ProjectID: projectID, Kind: "restore", InputHash: strings.Repeat("a", 64), IdempotencyKey: "empty-startup", RequestHash: strings.Repeat("b", 64), Status: sharedjob.Running, CreatedAt: now, UpdatedAt: now}
	journalStore := newRecoveryJournalStore(t)
	journal := ports.RestoreJournal{Version: backupdomain.RestoreJournalVersion, Job: job, Generation: 1, Phase: backupdomain.RestoreInstalled, ProjectID: projectID, BackupID: mustRecoveryID(), ManifestHash: strings.Repeat("c", 64), DatabaseHash: databaseHash, DatabaseBytes: databaseBytes, SchemaVersion: store.DBSchemaVersion(), CommandHash: job.RequestHash, PreflightGeneration: strings.Repeat("d", 64), TargetMode: backupdomain.RestoreEmptySelection, TargetPath: target, TargetIdentity: strings.Repeat("e", 64), TargetGeneration: strings.Repeat("f", 64), StagedPath: filepath.Join(target, ".eco-restore-"+string(jobID)+".staging"), StagedIdentity: databaseHash, InstalledPath: filepath.Join(target, "project.db"), InstalledIdentity: databaseHash, RestorePreNotApplicable: true, OriginalNotApplicable: true}
	if changed, err := journalStore.CompareAndSwap(ctx, 0, journal); err != nil || !changed {
		t.Fatalf("journal changed=%v err=%v", changed, err)
	}
	recent := project.NewFileRecentProjects(t.TempDir())
	recovery := &RestoreStartupRecovery{Journal: journalStore, Locker: project.FileLocker{}, Registry: registry, Replacement: restorefs.Replacement{Verifier: store.BackupVerifier{}}, Verifier: store.BackupVerifier{}, Recent: recent}
	if err = recovery.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	opened, openedID, err := store.OpenWithMigrationBackup(ctx, target, registry, nil)
	if err != nil || openedID != projectID {
		t.Fatalf("opened project=%s err=%v", openedID, err)
	}
	defer opened.Close()
	durable, err := opened.GetJob(ctx, jobID)
	if err != nil || durable.Status != sharedjob.Succeeded {
		t.Fatalf("durable job=%#v err=%v", durable, err)
	}
	values, err := recent.List()
	if err != nil || len(values) != 1 || values[0].ID != projectID || values[0].Path != target {
		t.Fatalf("recent=%#v err=%v", values, err)
	}
	if _, found, err := journalStore.Load(ctx, jobID); err != nil || found {
		t.Fatalf("terminal journal found=%v err=%v", found, err)
	}
}

func TestRestoreStartupRecoveryFailsJobWhileOriginalMainIsStillProven(t *testing.T) {
	ctx := context.Background()
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Clean(t.TempDir())
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	opened, projectID, err := store.Create(ctx, target, registry)
	if err != nil {
		t.Fatal(err)
	}
	job := createRunningRestoreJob(t, ctx, opened, projectID)
	if err = opened.Close(); err != nil {
		t.Fatal(err)
	}
	journalStore := newRecoveryJournalStore(t)
	journal := restoreJournalFixture(job, target, projectID, strings.Repeat("d", 64), 4096)
	journal.Phase = backupdomain.RestoreConnectionsClosed
	if changed, swapErr := journalStore.CompareAndSwap(ctx, 0, journal); swapErr != nil || !changed {
		t.Fatalf("create journal changed=%v err=%v", changed, swapErr)
	}
	recovery := &RestoreStartupRecovery{Journal: journalStore, Locker: project.FileLocker{}, Registry: registry, Replacement: restorefs.Replacement{Verifier: store.BackupVerifier{}}, Verifier: store.BackupVerifier{}}
	if err = recovery.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := store.OpenWithMigrationBackup(ctx, target, registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	durable, err := reopened.GetJob(ctx, job.ID)
	if err != nil || durable.Status != sharedjob.Failed {
		t.Fatalf("durable=%#v err=%v", durable, err)
	}
	events, err := reopened.ListEvents(ctx, job.ID, 0)
	if err != nil || len(events) != 1 || events[0].Phase != "recovered_rollback" || events[0].SafeError != "RESTORE_ROLLED_BACK" {
		t.Fatalf("rollback events=%#v err=%v", events, err)
	}
	if journal.RestorePreResult == nil {
		t.Fatal("restore-pre evidence was not journaled")
	}
	reconciledBackup, err := reopened.GetBackupResult(ctx, journal.RestorePreResult.BackupID)
	if err != nil || reconciledBackup.Source.CallerJobID != job.ID {
		t.Fatalf("rollback restore-pre=%#v err=%v", reconciledBackup, err)
	}
}

func TestInstalledOlderSchemaMigrationBackupFailureRollsBackOriginalBeforeProjectUse(t *testing.T) {
	ctx := context.Background()
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Clean(t.TempDir())
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	opened, projectID, err := store.Create(ctx, target, registry)
	if err != nil {
		t.Fatal(err)
	}
	entity, _, err := opened.Create(ctx, domain.KindTag, domain.EntityDraft{Key: "migration_rollback", Name: "Original", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	installedDirectory := filepath.Clean(t.TempDir())
	installedPath := filepath.Join(installedDirectory, "project.db")
	if err = (store.BackupSource{Store: opened, AppVersion: "test"}).OnlineBackup(ctx, installedPath, nil); err != nil {
		t.Fatal(err)
	}
	installedDB, err := sql.Open("sqlite", installedPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TABLE restore_graph_invalidations`, `DROP TABLE restore_reconciliation`, `DROP TABLE daily_backup_waivers`,
		`DROP TABLE daily_backup_admission`, `DROP TABLE backup_artifact_audit`, `DROP TABLE backup_commands`,
		`DELETE FROM schema_migration_steps WHERE step_id='backup-restore-v22'`, `UPDATE project_meta SET db_schema_version=21`,
	} {
		if _, err = installedDB.ExecContext(ctx, statement); err != nil {
			_ = installedDB.Close()
			t.Fatalf("prepare v21 %q: %v", statement, err)
		}
	}
	if err = installedDB.Close(); err != nil {
		t.Fatal(err)
	}
	job := createRunningRestoreJob(t, ctx, opened, projectID)
	if err = opened.Close(); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []string{filepath.Join(target, "project.db-wal"), filepath.Join(target, "project.db-shm")} {
		if removeErr := os.Remove(sidecar); removeErr != nil && !os.IsNotExist(removeErr) {
			t.Fatal(removeErr)
		}
	}
	originalPath := filepath.Join(target, ".eco-restore-"+string(job.ID)+".original")
	if err = os.Rename(filepath.Join(target, "project.db"), originalPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(installedPath, filepath.Join(target, "project.db")); err != nil {
		t.Fatal(err)
	}
	originalHash, _ := testFileHash(t, originalPath)
	installedHash, installedBytes := testFileHash(t, filepath.Join(target, "project.db"))
	journalStore := newRecoveryJournalStore(t)
	journal := restoreJournalFixture(job, target, projectID, installedHash, installedBytes)
	journal.Phase = backupdomain.RestoreInstalled
	journal.SchemaVersion = 21
	journal.StagedPath, journal.StagedIdentity = filepath.Join(target, ".eco-restore-"+string(job.ID)+".staging"), installedHash
	journal.OriginalPath, journal.OriginalIdentity = originalPath, originalHash
	journal.InstalledPath, journal.InstalledIdentity = filepath.Join(target, "project.db"), installedHash
	if changed, swapErr := journalStore.CompareAndSwap(ctx, 0, journal); swapErr != nil || !changed {
		t.Fatalf("create journal changed=%v err=%v", changed, swapErr)
	}
	migrationBackup := &failingRestoreMigrationBackup{}
	recovery := &RestoreStartupRecovery{Journal: journalStore, Locker: project.FileLocker{}, Registry: registry, MigrationBackup: migrationBackup, Replacement: restorefs.Replacement{Verifier: store.BackupVerifier{}}, Verifier: store.BackupVerifier{}}
	if err = recovery.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if migrationBackup.calls != 1 {
		t.Fatalf("migration backup calls=%d want=1", migrationBackup.calls)
	}
	reopened, reopenedID, err := store.OpenWithMigrationBackup(ctx, target, registry, nil)
	if err != nil || reopenedID != projectID {
		t.Fatalf("reopen project=%s err=%v", reopenedID, err)
	}
	defer reopened.Close()
	restored, err := reopened.Get(ctx, domain.KindTag, entity.ID)
	if err != nil || restored.Name != "Original" {
		t.Fatalf("restored original=%#v err=%v", restored, err)
	}
	durable, err := reopened.GetJob(ctx, job.ID)
	if err != nil || durable.Status != sharedjob.Failed {
		t.Fatalf("durable rollback job=%#v err=%v", durable, err)
	}
	identity, err := (store.BackupSource{Store: reopened, AppVersion: "test"}).Identity(ctx)
	if err != nil || identity.SchemaVersion != store.DBSchemaVersion() {
		t.Fatalf("restored identity=%#v err=%v", identity, err)
	}
	if _, found, loadErr := journalStore.Load(ctx, job.ID); loadErr != nil || found {
		t.Fatalf("terminal journal found=%v err=%v", found, loadErr)
	}
}

func createRunningRestoreJob(t *testing.T, ctx context.Context, opened *store.Store, projectID domain.ID) sharedjob.Record {
	t.Helper()
	hash := strings.Repeat("a", 64)
	job, replay, err := opened.CreateOrGet(ctx, sharedjob.Request{ProjectID: projectID, Kind: "restore", InputHash: hash, IdempotencyKey: "recovery-test", RequestHash: hash})
	if err != nil || replay {
		t.Fatalf("create restore job replay=%v err=%v", replay, err)
	}
	job, changed, err := opened.Transition(ctx, job.ID, sharedjob.Queued, sharedjob.Running, nil, 0)
	if err != nil || !changed {
		t.Fatalf("start restore job changed=%v err=%v", changed, err)
	}
	return job
}

func restoreJournalFixture(job sharedjob.Record, target string, projectID domain.ID, databaseHash string, databaseBytes int64) ports.RestoreJournal {
	restorePreID := mustRecoveryID()
	restorePreResult := backupdomain.Result{
		EvidenceVersion: backupdomain.EvidenceVersion, BackupID: restorePreID, ProjectID: projectID,
		Type: backupdomain.RestorePre, Trigger: backupdomain.TriggerRestore, CreatedAt: job.CreatedAt,
		AppVersion: "test", SchemaVersion: store.DBSchemaVersion(), DBBytes: 4096,
		DBSHA256: strings.Repeat("9", 64), ManifestHash: strings.Repeat("8", 64), Integrity: "ok",
		Source:    backupdomain.SourceIdentity{CallerJobID: job.ID, RequestHash: job.RequestHash},
		ResultURL: "/api/v1/backups/" + string(restorePreID),
	}
	return ports.RestoreJournal{
		Version: backupdomain.RestoreJournalVersion, Job: job, Generation: 1,
		Phase: backupdomain.RestorePreflighted, ProjectID: projectID, BackupID: mustRecoveryID(),
		ManifestHash: strings.Repeat("b", 64), DatabaseHash: databaseHash, DatabaseBytes: databaseBytes,
		SchemaVersion: store.DBSchemaVersion(), CommandHash: job.RequestHash,
		PreflightGeneration: strings.Repeat("c", 64), TargetMode: backupdomain.RestoreActive,
		TargetPath: target, TargetIdentity: strings.Repeat("e", 64), TargetGeneration: strings.Repeat("f", 64),
		RestorePreResult: &restorePreResult,
	}
}

func newRecoveryJournalStore(t *testing.T) *restorejournal.Store {
	t.Helper()
	value, err := restorejournal.New(filepath.Join(filepath.Clean(t.TempDir()), "restore"))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustRecoveryID() domain.ID {
	value, err := domain.NewID()
	if err != nil {
		panic(err)
	}
	return value
}

func testFileHash(t *testing.T, path string) (string, int64) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	size, err := io.Copy(digest, file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), size
}

var errInjectedJournalDelete = errors.New("injected journal delete failure")

type deleteFailJournal struct{ ports.RestoreJournalStore }

func (*deleteFailJournal) DeleteTerminal(context.Context, domain.ID, int64) error {
	return errInjectedJournalDelete
}
