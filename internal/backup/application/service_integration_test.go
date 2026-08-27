package application_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	backupintegration "github.com/zouyi/eco-guardian/internal/backup/integration"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	versioning "github.com/zouyi/eco-guardian/internal/versioning"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

func backupServiceFixture(t *testing.T) (*application.Service, *store.Store, *backupfs.Store) {
	t.Helper()
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	project, _, err := store.Create(context.Background(), t.TempDir(), registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = project.Close() })
	if _, _, err = project.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "backup_fixture", Name: "Backup", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}}); err != nil {
		t.Fatal(err)
	}
	artifacts, err := backupfs.NewStore(filepath.Clean(t.TempDir()), store.DBSchemaVersion())
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store.BackupSource{Store: project, AppVersion: "test"}, artifacts, store.BackupVerifier{}, project, project, backupfs.Probe{})
	return service, project, artifacts
}

func TestDailyAdmissionBlocksUntilOneArtifactAndSystemWritesDoNotRecurse(t *testing.T) {
	service, project, artifacts := backupServiceFixture(t)
	now := time.Date(2026, 8, 26, 2, 3, 4, 0, time.UTC)
	clock := application.ClockFunc(func() time.Time { return now })
	service.Clock = clock
	admission := &application.DailyAdmission{ProjectID: project.ProjectID(), Backups: service, Jobs: project, State: project, Clock: clock, Location: time.FixedZone("fixture", 8*60*60)}
	if err := project.RegisterBusinessWriteAdmission(admission.AdmitBusinessWrite); err != nil {
		t.Fatal(err)
	}
	if _, _, err := project.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "daily_one", Name: "Daily", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := project.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "daily_two", Name: "Daily 2", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}}); err != nil {
		t.Fatal(err)
	}
	page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if err != nil || len(page.Items) != 1 || page.Items[0].Type != backupdomain.Daily {
		t.Fatalf("inventory=%#v err=%v", page, err)
	}
	record, found, err := project.GetDailyAdmission(context.Background(), project.ProjectID(), "2026-08-26")
	if err != nil || !found {
		t.Fatalf("daily record=%#v found=%v err=%v", record, found, err)
	}
	job, err := project.GetJob(context.Background(), record.JobID)
	if err != nil || job.Status != "succeeded" {
		t.Fatalf("daily job=%#v err=%v", job, err)
	}
}

func TestConcurrentFirstBusinessWritesReuseOneDailyBackupBeforeCommit(t *testing.T) {
	service, project, artifacts := backupServiceFixture(t)
	now := time.Date(2026, 8, 26, 3, 0, 0, 0, time.UTC)
	clock := application.ClockFunc(func() time.Time { return now })
	service.Clock = clock
	admission := &application.DailyAdmission{ProjectID: project.ProjectID(), Backups: service, Jobs: project, State: project, Clock: clock, Location: time.UTC}
	if err := project.RegisterBusinessWriteAdmission(admission.AdmitBusinessWrite); err != nil {
		t.Fatal(err)
	}
	const writers = 8
	start := make(chan struct{})
	errorsByWriter := make(chan error, writers)
	var group sync.WaitGroup
	for index := 0; index < writers; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, _, err := project.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: fmt.Sprintf("daily_concurrent_%d", index), Name: "Concurrent", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
			errorsByWriter <- err
		}(index)
	}
	close(start)
	group.Wait()
	close(errorsByWriter)
	for err := range errorsByWriter {
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if err != nil || len(page.Items) != 1 || page.Items[0].Type != backupdomain.Daily {
		t.Fatalf("inventory=%#v err=%v", page, err)
	}
	record, found, err := project.GetDailyAdmission(context.Background(), project.ProjectID(), "2026-08-26")
	if err != nil || !found {
		t.Fatalf("daily record=%#v found=%v err=%v", record, found, err)
	}
	events, err := project.ListEvents(context.Background(), record.JobID, 0)
	if err != nil || len(events) == 0 || events[len(events)-1].Progress != 100 {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}

func TestPostPublicationRetentionKeepsNewestDailyArtifactsOnly(t *testing.T) {
	service, project, artifacts := backupServiceFixture(t)
	service.Retention = application.RetentionPolicy{Daily: 2, ReleaseMigration: 5}
	base := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		now := base.AddDate(0, 0, index)
		service.Clock = application.ClockFunc(func() time.Time { return now })
		command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: project.ProjectID(), Purpose: backupdomain.Daily, LocalDate: now.Format("2006-01-02"), Source: backupdomain.SourceIdentity{}}
		job, _, err := service.Submit(context.Background(), command, "retention-"+now.Format("2006-01-02"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = service.ExecuteStored(context.Background(), job.ID); err != nil {
			t.Fatal(err)
		}
	}
	page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("inventory=%#v err=%v", page, err)
	}
	if !page.Items[0].CreatedAt.After(page.Items[1].CreatedAt) || page.Items[1].CreatedAt != base.AddDate(0, 0, 1) {
		t.Fatalf("wrong retained artifacts: %#v", page.Items)
	}
}

func TestFailedBackupAtQuotaDoesNotEvictValidArtifacts(t *testing.T) {
	service, project, artifacts := backupServiceFixture(t)
	service.Retention = application.RetentionPolicy{Daily: 2, ReleaseMigration: 5}
	base := time.Date(2026, 8, 24, 1, 0, 0, 0, time.UTC)
	for index := 0; index < 2; index++ {
		now := base.AddDate(0, 0, index)
		service.Clock = application.ClockFunc(func() time.Time { return now })
		command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: project.ProjectID(), Purpose: backupdomain.Daily, LocalDate: now.Format("2006-01-02"), Source: backupdomain.SourceIdentity{}}
		job, _, err := service.Submit(context.Background(), command, fmt.Sprintf("quota-%d", index))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = service.ExecuteStored(context.Background(), job.ID); err != nil {
			t.Fatal(err)
		}
	}
	service.Source = failingSnapshotSource{identity: ports.SnapshotIdentity{ProjectID: project.ProjectID(), SchemaVersion: store.DBSchemaVersion(), AppVersion: "test"}}
	failedAt := base.AddDate(0, 0, 2)
	command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: project.ProjectID(), Purpose: backupdomain.Daily, LocalDate: failedAt.Format("2006-01-02"), Source: backupdomain.SourceIdentity{}}
	job, _, err := service.Submit(context.Background(), command, "quota-failed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ExecuteStored(context.Background(), job.ID); err == nil {
		t.Fatal("injected quota backup unexpectedly succeeded")
	}
	page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("failed backup evicted valid quota artifacts=%#v err=%v", page.Items, err)
	}
}

type failingSnapshotSource struct{ identity ports.SnapshotIdentity }

func (source failingSnapshotSource) Identity(context.Context) (ports.SnapshotIdentity, error) {
	return source.identity, errors.New("injected backup failure")
}
func (failingSnapshotSource) OnlineBackup(context.Context, string, func(ports.BackupProgress) error) error {
	return errors.New("injected backup failure")
}

type retryableSnapshotSource struct {
	next ports.ProjectSnapshotSource
	fail bool
}

func (source *retryableSnapshotSource) Identity(ctx context.Context) (ports.SnapshotIdentity, error) {
	return source.next.Identity(ctx)
}
func (source *retryableSnapshotSource) OnlineBackup(ctx context.Context, path string, progress func(ports.BackupProgress) error) error {
	if source.fail {
		return errors.New("injected daily failure")
	}
	return source.next.OnlineBackup(ctx, path, progress)
}

func TestDailyFailureRequiresExplicitSameDateWaiverBeforeBusinessWrite(t *testing.T) {
	_, project, artifacts := backupServiceFixture(t)
	now := time.Date(2026, 8, 26, 2, 3, 4, 0, time.UTC)
	clock := application.ClockFunc(func() time.Time { return now })
	service := application.NewService(failingSnapshotSource{identity: ports.SnapshotIdentity{ProjectID: project.ProjectID(), SchemaVersion: store.DBSchemaVersion(), AppVersion: "test"}}, artifacts, store.BackupVerifier{}, project, project, backupfs.Probe{})
	service.Clock = clock
	admission := &application.DailyAdmission{ProjectID: project.ProjectID(), Backups: service, Jobs: project, State: project, Clock: clock, Location: time.UTC}
	if err := project.RegisterBusinessWriteAdmission(admission.AdmitBusinessWrite); err != nil {
		t.Fatal(err)
	}
	create := func() error {
		_, _, err := project.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "waived_write", Name: "Waived", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
		return err
	}
	err := create()
	var required application.DailyRequiredError
	if !errors.As(err, &required) || !required.JobID.Valid() {
		t.Fatalf("write err=%v", err)
	}
	if _, _, err = admission.ConfirmWaiver(context.Background(), required.JobID, "local-user"); err != nil {
		t.Fatal(err)
	}
	if err = create(); err != nil {
		t.Fatalf("explicitly waived write failed: %v", err)
	}
	page, _ := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if len(page.Items) != 0 {
		t.Fatalf("failed daily backup published an artifact: %#v", page.Items)
	}
	now = now.Add(24 * time.Hour)
	if err = create(); !errors.As(err, &required) {
		t.Fatalf("next date reused waiver: %v", err)
	}
}

func TestDailyRetryReplacesOnlyTheBoundFailedAttemptAndSucceedsBeforeWrite(t *testing.T) {
	_, project, artifacts := backupServiceFixture(t)
	now := time.Date(2026, 8, 26, 2, 3, 4, 0, time.UTC)
	clock := application.ClockFunc(func() time.Time { return now })
	source := &retryableSnapshotSource{next: store.BackupSource{Store: project, AppVersion: "test"}, fail: true}
	service := application.NewService(source, artifacts, store.BackupVerifier{}, project, project, backupfs.Probe{})
	service.Clock = clock
	admission := &application.DailyAdmission{ProjectID: project.ProjectID(), Backups: service, Jobs: project, State: project, Clock: clock, Location: time.UTC}
	err := admission.AdmitBusinessWrite(context.Background())
	var required application.DailyRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("first attempt err=%v", err)
	}
	failedID := required.JobID
	source.fail = false
	retried, err := admission.RetryFailed(context.Background(), failedID)
	if err != nil || retried.Status != "succeeded" || retried.ID == failedID {
		t.Fatalf("retried=%#v err=%v", retried, err)
	}
	record, found, err := project.GetDailyAdmission(context.Background(), project.ProjectID(), "2026-08-26")
	if err != nil || !found || record.JobID != retried.ID {
		t.Fatalf("daily admission=%#v found=%v err=%v", record, found, err)
	}
	if err = admission.AdmitBusinessWrite(context.Background()); err != nil {
		t.Fatalf("successful retry did not admit retained write: %v", err)
	}
	page, _ := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if len(page.Items) != 1 || page.Items[0].Type != backupdomain.Daily {
		t.Fatalf("retry artifacts=%#v", page.Items)
	}
}

func manualCommand(projectID domain.ID, reason string) backupdomain.Command {
	return backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: projectID, Purpose: backupdomain.Manual, ManualReason: reason, Source: backupdomain.SourceIdentity{}}
}

func TestServicePublishesOneDurableIdempotentManualBackup(t *testing.T) {
	service, project, artifacts := backupServiceFixture(t)
	command := manualCommand(project.ProjectID(), "manual")
	job, replay, err := service.Submit(context.Background(), command, "manual-key")
	if err != nil || replay {
		t.Fatalf("job=%#v replay=%v err=%v", job, replay, err)
	}
	result, err := service.ExecuteStored(context.Background(), job.ID)
	if err != nil || !result.Valid() {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	stored, err := project.GetJob(context.Background(), job.ID)
	if err != nil || stored.Status != "succeeded" || stored.Result == nil || stored.Result.ID != result.BackupID {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	events, err := project.ListEvents(context.Background(), job.ID, 0)
	if err != nil || len(events) < 3 || events[len(events)-1].Progress != 100 || events[len(events)-1].Result == nil {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if err != nil || len(page.Items) != 1 || page.Items[0].BackupID != result.BackupID || !page.Items[0].Restorable() {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	audit, err := project.GetBackupResult(context.Background(), result.BackupID)
	if err != nil || audit != result {
		t.Fatalf("audit=%#v err=%v", audit, err)
	}
	storedCommand, err := project.GetBackupCommand(context.Background(), job.ID)
	if err != nil || storedCommand != command {
		t.Fatalf("command=%#v err=%v", storedCommand, err)
	}
	replayed, replay, err := service.Submit(context.Background(), command, "manual-key")
	if err != nil || !replay || replayed.ID != job.ID {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, err)
	}
	if _, err = service.ExecuteStored(context.Background(), job.ID); err != nil {
		t.Fatalf("terminal replay failed: %v", err)
	}
	page, _ = artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if len(page.Items) != 1 {
		t.Fatalf("terminal replay created duplicate artifacts: %#v", page.Items)
	}
	changed := manualCommand(project.ProjectID(), "changed")
	if _, _, err = service.Submit(context.Background(), changed, "manual-key"); !errors.Is(err, store.ErrJobIdempotencyConflict) {
		t.Fatalf("changed input err=%v", err)
	}
}

func TestRestartedConcurrentManualWorkersReuseOneArtifactResultAndEventStream(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	projectDir := filepath.Join(t.TempDir(), "project")
	project, _, err := store.Create(context.Background(), projectDir, registry)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = project.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: "restart_claim", Name: "Restart claim", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}}); err != nil {
		t.Fatal(err)
	}
	artifacts, err := backupfs.NewStore(filepath.Clean(t.TempDir()), store.DBSchemaVersion())
	if err != nil {
		t.Fatal(err)
	}
	beforeRestart := application.NewService(store.BackupSource{Store: project, AppVersion: "test"}, artifacts, store.BackupVerifier{}, project, project, backupfs.Probe{})
	command := manualCommand(project.ProjectID(), "restart-concurrent-claim")
	job, replay, err := beforeRestart.Submit(context.Background(), command, "restart-concurrent-claim")
	if err != nil || replay {
		t.Fatalf("job=%#v replay=%v err=%v", job, replay, err)
	}
	if _, changed, transitionErr := project.Transition(context.Background(), job.ID, job.Status, "running", nil, 0); transitionErr != nil || !changed {
		t.Fatalf("persist running changed=%v err=%v", changed, transitionErr)
	}
	if err = project.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, reopenedID, err := store.Open(projectDir, registry)
	if err != nil || reopenedID != command.ProjectID {
		t.Fatalf("reopened id=%s err=%v", reopenedID, err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	afterRestart := application.NewService(store.BackupSource{Store: reopened, AppVersion: "test"}, artifacts, store.BackupVerifier{}, reopened, reopened, backupfs.Probe{})

	const workers = 8
	start := make(chan struct{})
	results := make(chan backupdomain.Result, workers)
	errorsByWorker := make(chan error, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, executeErr := afterRestart.ExecuteStored(context.Background(), job.ID)
			results <- result
			errorsByWorker <- executeErr
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsByWorker)
	for executeErr := range errorsByWorker {
		if executeErr != nil {
			t.Fatal(executeErr)
		}
	}
	var backupID domain.ID
	for result := range results {
		if !result.Valid() {
			t.Fatalf("invalid replayed result: %#v", result)
		}
		if backupID == "" {
			backupID = result.BackupID
		} else if result.BackupID != backupID {
			t.Fatalf("workers returned different artifacts: %s and %s", backupID, result.BackupID)
		}
	}
	page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: command.ProjectID})
	if err != nil || len(page.Items) != 1 || page.Items[0].BackupID != backupID {
		t.Fatalf("inventory=%#v err=%v", page, err)
	}
	events, err := reopened.ListEvents(context.Background(), job.ID, 0)
	if err != nil || len(events) == 0 {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	resultEvents := 0
	for index, event := range events {
		if event.JobID != job.ID || event.Ordinal != int64(index+1) {
			t.Fatalf("event identity/ordinal at %d: %#v", index, event)
		}
		if event.Result != nil {
			resultEvents++
			if event.Result.ID != backupID || event.Result.URL == "" {
				t.Fatalf("result event=%#v", event)
			}
		}
	}
	if resultEvents != 1 {
		t.Fatalf("result-bearing events=%d want=1", resultEvents)
	}
	database, err := sql.Open("sqlite", filepath.Join(projectDir, "project.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var resultSeals int
	if err = database.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM backup_artifact_audit WHERE job_id=?`, job.ID).Scan(&resultSeals); err != nil || resultSeals != 1 {
		t.Fatalf("result seals=%d err=%v", resultSeals, err)
	}
}

func TestServiceCancellationBeforeCopyPublishesNothing(t *testing.T) {
	service, project, artifacts := backupServiceFixture(t)
	command := manualCommand(project.ProjectID(), "cancel")
	job, _, err := service.Submit(context.Background(), command, "cancel-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = project.RequestCancellation(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Execute(context.Background(), job.ID, command); !errors.Is(err, context.Canceled) {
		t.Fatalf("execute err=%v", err)
	}
	stored, _ := project.GetJob(context.Background(), job.ID)
	if stored.Status != "canceled" {
		t.Fatalf("stored status=%s", stored.Status)
	}
	page, _ := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if len(page.Items) != 0 {
		t.Fatalf("canceled backup published artifacts: %#v", page.Items)
	}
}

func TestCancellationIsDeniedAfterDurablePublicationBoundary(t *testing.T) {
	service, project, _ := backupServiceFixture(t)
	command := manualCommand(project.ProjectID(), "publication-boundary")
	job, _, err := service.Submit(context.Background(), command, "publication-boundary-key")
	if err != nil {
		t.Fatal(err)
	}
	job, changed, err := project.Transition(context.Background(), job.ID, "queued", "running", nil, 0)
	if err != nil || !changed {
		t.Fatalf("running=%#v changed=%v err=%v", job, changed, err)
	}
	if begun, beginErr := project.BeginBackupPublication(context.Background(), job.ID, 0, time.Now().UTC()); beginErr != nil || !begun {
		t.Fatalf("begun=%v err=%v", begun, beginErr)
	}
	current, denied, err := project.RequestCancellation(context.Background(), job.ID)
	if err != nil || !denied || current.CancelGeneration != 0 || current.Status != "running" {
		t.Fatalf("current=%#v denied=%v err=%v", current, denied, err)
	}
}

func TestMandatoryDirectBackupBindsCallerEvidenceWithoutBackupTables(t *testing.T) {
	service, project, _ := backupServiceFixture(t)
	requestHash := strings.Repeat("b", 64)
	callerJob, _, err := project.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: project.ProjectID(), Kind: "migration", InputHash: requestHash, IdempotencyKey: "migration-caller", RequestHash: requestHash})
	if err != nil {
		t.Fatal(err)
	}
	caller := callerJob.ID
	command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: project.ProjectID(), Purpose: backupdomain.Migration, CallerJobID: caller, CallerHash: requestHash, Source: backupdomain.SourceIdentity{CallerJobID: caller, RequestHash: requestHash, MigrationID: "schema-22"}}
	result, err := service.ExecuteDirect(context.Background(), command, nil)
	if err != nil || !result.Valid() || result.Source.CallerJobID != caller || result.Type != backupdomain.Migration {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	identity, err := service.Source.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(identity.ProjectPath, "project.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var sealedCaller string
	if err = database.QueryRowContext(context.Background(), `SELECT job_id FROM backup_artifact_audit WHERE backup_id=?`, result.BackupID).Scan(&sealedCaller); err != nil || sealedCaller != string(caller) {
		t.Fatalf("sealed caller=%q err=%v", sealedCaller, err)
	}
}

func TestMandatoryInvokerIgnoresDailyWaiverAndHasNoReleaseOverridePath(t *testing.T) {
	_, project, artifacts := backupServiceFixture(t)
	now := time.Date(2026, 8, 26, 2, 3, 4, 0, time.UTC)
	clock := application.ClockFunc(func() time.Time { return now })
	service := application.NewService(failingSnapshotSource{identity: ports.SnapshotIdentity{ProjectID: project.ProjectID(), SchemaVersion: store.DBSchemaVersion(), AppVersion: "test"}}, artifacts, store.BackupVerifier{}, project, project, backupfs.Probe{})
	service.Clock = clock
	admission := &application.DailyAdmission{ProjectID: project.ProjectID(), Backups: service, Jobs: project, State: project, Clock: clock, Location: time.UTC}
	err := admission.AdmitBusinessWrite(context.Background())
	var required application.DailyRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("daily failure=%v", err)
	}
	if _, _, err = admission.ConfirmWaiver(context.Background(), required.JobID, "local-user"); err != nil {
		t.Fatal(err)
	}
	invoker := application.MandatoryInvoker{Service: func() *application.Service { return service }}
	caller, _ := domain.NewID()
	requestHash := strings.Repeat("7", 64)
	for _, purpose := range []backupdomain.Type{backupdomain.Migration, backupdomain.RestorePre, backupdomain.Release} {
		command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: project.ProjectID(), Purpose: purpose, CallerJobID: caller, CallerHash: requestHash, Source: backupdomain.SourceIdentity{CallerJobID: caller, RequestHash: requestHash}}
		if _, invokeErr := invoker.Invoke(context.Background(), command); invokeErr == nil {
			t.Fatalf("%s mandatory failure was bypassed", purpose)
		}
	}
	page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("mandatory failure artifacts=%#v err=%v", page.Items, err)
	}
	// Warning confirmations, release notes, degraded-runtime state and numeric
	// overrides are deliberately absent from BackupInvoker.Invoke's signature;
	// therefore none can alter the mandatory command or its failed result.
}

func TestReleaseGateMandatoryBackupIsNonOverridableAndIdempotent(t *testing.T) {
	service, project, artifacts := backupServiceFixture(t)
	gate := backupintegration.NewReleaseBackupGate(func() *application.Service { return service })
	requestHash := strings.Repeat("d", 64)
	callerJob, _, err := project.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: project.ProjectID(), Kind: "release", InputHash: requestHash, IdempotencyKey: "release-gate-caller", RequestHash: requestHash})
	if err != nil {
		t.Fatal(err)
	}
	jobID := callerJob.ID
	for attempt := 0; attempt < 2; attempt++ {
		evidence, err := gate.Backup(context.Background(), jobID, requestHash)
		if err != nil || !evidence.Valid() {
			t.Fatalf("attempt=%d evidence=%#v err=%v", attempt, evidence, err)
		}
	}
	// Rebuild the application/gate objects with no retained in-memory state.
	// Mandatory identity is recovered solely from the published artifact.
	restarted := application.NewService(service.Source, artifacts, service.Verifier, project, project, service.Space)
	restartedGate := backupintegration.NewReleaseBackupGate(func() *application.Service { return restarted })
	if evidence, restartErr := restartedGate.Backup(context.Background(), jobID, requestHash); restartErr != nil || !evidence.Valid() {
		t.Fatalf("restart evidence=%#v err=%v", evidence, restartErr)
	}
	page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if err != nil || len(page.Items) != 1 || page.Items[0].Type != backupdomain.Release {
		t.Fatalf("inventory=%#v err=%v", page, err)
	}
	if _, err = versioningrelease.PerformMandatoryBackup(context.Background(), backupintegration.ReleaseBackupGate{}, versioningrelease.Job{}); !errors.Is(err, versioningrelease.ErrMandatoryBackupFailed) {
		t.Fatalf("missing gate err=%v", err)
	}
}

func TestReleaseBackupGateAllowsIntentOnlyAfterVerifiedEvidence(t *testing.T) {
	service, project, artifacts := backupServiceFixture(t)
	job, policyID := releaseJobFixture(t, project, "release-gate-success")
	gate := backupintegration.NewReleaseBackupGate(func() *application.Service { return service })
	worker := versioningrelease.Worker{Jobs: project, Lane: versioningrelease.NewWriteLane()}
	var intentID domain.ID
	completed, err := worker.Run(context.Background(), job.ID, func(ctx context.Context, running versioningrelease.Job) (versioningrelease.JobResult, error) {
		evidence, backupErr := versioningrelease.PerformMandatoryBackup(ctx, gate, running)
		if backupErr != nil {
			return versioningrelease.JobResult{}, backupErr
		}
		intentID, _ = domain.NewID()
		manifest := []byte(`{"gates":["backup"]}`)
		now := time.Now().UTC()
		intent := versioningrelease.Intent{ID: intentID, JobID: running.ID, CandidateRevisionID: running.RevisionID, PolicyID: policyID, GateManifest: manifest, GateManifestHash: versioning.SHA256(manifest), Backup: evidence, RequestHash: running.RequestHash, IdempotencyKey: running.IdempotencyKey, Phase: versioningrelease.IntentRecorded, CreatedAt: now, UpdatedAt: now}
		if _, _, createErr := project.CreateIntent(ctx, intent); createErr != nil {
			return versioningrelease.JobResult{}, createErr
		}
		return versioningrelease.JobResult{Type: "release", ID: intentID, URL: "/api/v1/releases/" + string(intentID)}, nil
	})
	if err != nil || completed.Status != versioningrelease.JobSucceeded {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	if stored, getErr := project.GetIntent(context.Background(), intentID); getErr != nil || stored.JobID != job.ID || !stored.Backup.Valid() {
		t.Fatalf("intent=%#v err=%v", stored, getErr)
	}
	page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: project.ProjectID()})
	if err != nil || len(page.Items) != 1 || page.Items[0].Type != backupdomain.Release {
		t.Fatalf("release backup inventory=%#v err=%v", page, err)
	}
}

func TestReleaseBackupGateFailureMatrixStopsBeforeIntentGraphReleaseAndPointer(t *testing.T) {
	for _, test := range []struct {
		name string
		gate versioningrelease.BackupGate
	}{
		{name: "missing"},
		{name: "failed", gate: releaseBackupGateFake{err: errors.New("backup failed")}},
		{name: "stale", gate: releaseBackupGateFake{evidence: versioningrelease.BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("x", 64)}}},
		{name: "incompatible", gate: releaseBackupGateFake{evidence: versioningrelease.BackupEvidence{Online: true, IntegrityChecked: false, Checksum: strings.Repeat("a", 64)}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, project, _ := backupServiceFixture(t)
			job, _ := releaseJobFixture(t, project, "release-gate-"+test.name)
			worker := versioningrelease.Worker{Jobs: project, Lane: versioningrelease.NewWriteLane()}
			var intent, graph, release, pointer int
			completed, err := worker.Run(context.Background(), job.ID, func(ctx context.Context, running versioningrelease.Job) (versioningrelease.JobResult, error) {
				if _, backupErr := versioningrelease.PerformMandatoryBackup(ctx, test.gate, running); backupErr != nil {
					return versioningrelease.JobResult{}, backupErr
				}
				intent++
				graph++
				release++
				pointer++
				return versioningrelease.JobResult{}, nil
			})
			if !errors.Is(err, versioningrelease.ErrMandatoryBackupFailed) || completed.Status != versioningrelease.JobFailed || intent+graph+release+pointer != 0 {
				t.Fatalf("completed=%#v effects=%d/%d/%d/%d err=%v", completed, intent, graph, release, pointer, err)
			}
		})
	}
}

type releaseBackupGateFake struct {
	evidence versioningrelease.BackupEvidence
	err      error
}

func (fake releaseBackupGateFake) Backup(context.Context, domain.ID, string) (versioningrelease.BackupEvidence, error) {
	return fake.evidence, fake.err
}

type releasePolicyCatalog struct{}

func (releasePolicyCatalog) SupportsCapabilityContract(versioningpolicy.CapabilityRequirement) bool {
	return true
}

func releaseJobFixture(t *testing.T, project *store.Store, key string) (versioningrelease.Job, domain.ID) {
	t.Helper()
	entityKey := strings.ReplaceAll(key, "-", "_")
	_, revision, err := project.Create(context.Background(), domain.KindTag, domain.EntityDraft{Key: entityKey, Name: "Release", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := project.CreatePolicy(context.Background(), versioningpolicy.Definition{Samples: 1, ThresholdID: "threshold", ThresholdOn: true, Scenes: []versioningpolicy.Scene{{ID: "scene", Required: true, Metrics: []versioningpolicy.Metric{{ID: "metric", Required: true}}}}, Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: "backup", GateID: "mandatory-consistent-backup", ContractVersion: "backup-gate-v1"}}}, releasePolicyCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	requestHash := strings.Repeat("e", 64)
	job, _, err := project.CreateOrGetReleaseJob(context.Background(), versioningrelease.JobRequest{ProjectID: project.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: key, RequestHash: requestHash})
	if err != nil {
		t.Fatal(err)
	}
	return job, policy.ID
}
