package application_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	backupintegration "github.com/zouyi/eco-guardian/internal/backup/integration"
	"github.com/zouyi/eco-guardian/internal/backup/restorefs"
	"github.com/zouyi/eco-guardian/internal/backup/restorejournal"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioning "github.com/zouyi/eco-guardian/internal/versioning"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

type restoreSelector string

func (selector restoreSelector) SelectDirectory(context.Context) (string, error) {
	return string(selector), nil
}

func TestActiveRestoreReplacesDatabaseRehydratesOriginalJobAndReopensProject(t *testing.T) {
	ctx := context.Background()
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	projectDirectory := filepath.Clean(t.TempDir())
	descriptor := projector.Descriptor{SchemaVersion: projector.ProjectionSchemaV1, Version: projector.ProjectorV1, Relations: projector.V1Relations(), Formatter: projector.V1Formatter{}}
	manager := project.NewManager(project.NewTokenStore(time.Minute, nil), project.FileLocker{}, project.SQLiteFactory{Registry: registry, GraphVersionContributor: projector.VersionContributor{Descriptor: descriptor}}, project.NoJobs{}, nil)
	token, _, err := manager.IssueSelection(ctx, restoreSelector(projectDirectory))
	if err != nil {
		t.Fatal(err)
	}
	info, err := manager.Create(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	handle, _ := manager.ActiveHandle()
	opened := handle.(interface{ Store() *store.Store }).Store()
	entity, workingRevision, err := opened.Create(ctx, domain.KindTag, domain.EntityDraft{Key: "restore_e2e", Name: "Before", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := opened.CreateCheckpoint(ctx, workingRevision.ID, "Restore checkpoint", "Immutable timepoint evidence")
	if err != nil {
		t.Fatal(err)
	}
	validationReport, err := opened.RunValidation(ctx, validation.SourceRevision, checkpoint.ID, validation.ScopeFull)
	if err != nil {
		t.Fatal(err)
	}
	if err = opened.CreateGraphSyncState(ctx, graphsync.SyncState{RevisionID: string(checkpoint.ID), Pipeline: graphsync.StateReady, Warnings: []string{}}); err != nil {
		t.Fatal(err)
	}
	auditHash := strings.Repeat("7", 64)
	auditJob, _, err := opened.CreateOrGet(ctx, sharedjob.Request{ProjectID: info.ID, Kind: "timepoint-audit", InputHash: auditHash, IdempotencyKey: "timepoint-audit", RequestHash: auditHash})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = opened.Append(ctx, sharedjob.Event{JobID: auditJob.ID, Ordinal: 1, Phase: "recorded", Progress: 100, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	policyID, _ := domain.NewID()
	projectDatabase, err := sql.Open("sqlite", filepath.Join(projectDirectory, "project.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = projectDatabase.ExecContext(ctx, `INSERT INTO release_policies(id,display_version,canonical_body,canonical_hash,created_at) VALUES(?,?,?,?,?)`, policyID, 999, `{}`, strings.Repeat("6", 64), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		_ = projectDatabase.Close()
		t.Fatal(err)
	}
	if err = projectDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	releaseHash := strings.Repeat("5", 64)
	releaseJob, _, err := opened.CreateOrGetReleaseJob(ctx, versioningrelease.JobRequest{ProjectID: info.ID, RevisionID: checkpoint.ID, InputHash: checkpoint.ConfigHash, IdempotencyKey: "timepoint-release", RequestHash: releaseHash})
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"gates":[]}`)
	now := time.Now().UTC()
	intentID, _ := domain.NewID()
	intent := versioningrelease.Intent{ID: intentID, JobID: releaseJob.ID, CandidateRevisionID: checkpoint.ID, PolicyID: policyID, GateManifest: manifest, GateManifestHash: versioning.SHA256(manifest), Backup: versioningrelease.BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("4", 64)}, RequestHash: releaseJob.RequestHash, IdempotencyKey: releaseJob.IdempotencyKey, Phase: versioningrelease.IntentRecorded, CreatedAt: now, UpdatedAt: now}
	if _, _, err = opened.CreateIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if _, changed, transitionErr := opened.TransitionReleaseJob(ctx, releaseJob.ID, versioningrelease.JobQueued, versioningrelease.JobRunning, nil); transitionErr != nil || !changed {
		t.Fatalf("release running changed=%v err=%v", changed, transitionErr)
	}
	if _, changed, transitionErr := opened.TransitionIntent(ctx, intent.ID, versioningrelease.IntentRecorded, versioningrelease.IntentGraphActivating, "restore-timepoint-task", ""); transitionErr != nil || !changed {
		t.Fatalf("intent activating changed=%v err=%v", changed, transitionErr)
	}
	if _, changed, transitionErr := opened.TransitionIntent(ctx, intent.ID, versioningrelease.IntentGraphActivating, versioningrelease.IntentGraphActivated, "restore-timepoint-task", ""); transitionErr != nil || !changed {
		t.Fatalf("intent activated changed=%v err=%v", changed, transitionErr)
	}
	release, _, err := opened.CommitActivatedIntent(ctx, intent.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, transitionErr := opened.TransitionReleaseJob(ctx, releaseJob.ID, versioningrelease.JobRunning, versioningrelease.JobSucceeded, &versioningrelease.JobResult{Type: "release", ID: release.ID, URL: "/api/v1/releases/" + string(release.ID)}); transitionErr != nil || !changed {
		t.Fatalf("release succeeded changed=%v err=%v", changed, transitionErr)
	}
	artifactRoot := filepath.Clean(t.TempDir())
	artifacts, err := backupfs.NewStore(artifactRoot, store.DBSchemaVersion())
	if err != nil {
		t.Fatal(err)
	}
	backups := application.NewService(store.BackupSource{Store: opened, AppVersion: "test"}, artifacts, store.BackupVerifier{}, opened, opened, backupfs.Probe{})
	command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: info.ID, Purpose: backupdomain.Manual, ManualReason: "restore point", Source: backupdomain.SourceIdentity{}}
	backupJob, _, err := backups.Submit(ctx, command, "restore-point")
	if err != nil {
		t.Fatal(err)
	}
	backupResult, err := backups.ExecuteStored(ctx, backupJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = opened.Patch(ctx, domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"After"`)}); err != nil {
		t.Fatal(err)
	}
	journalStore, err := restorejournal.New(filepath.Join(filepath.Clean(t.TempDir()), "runtime", "restore"))
	if err != nil {
		t.Fatal(err)
	}
	restores := application.NewRestoreService(backups, backupintegration.NewProjectTargets(manager, nil), journalStore, backupintegration.ProjectMaintenance{Manager: manager}, restorefs.Replacement{Verifier: store.BackupVerifier{}})
	preflight, err := restores.Preflight(ctx, backupResult.BackupID, backupdomain.RestoreActive)
	if err != nil || !preflight.Valid(time.Now().UTC()) {
		t.Fatalf("preflight=%#v err=%v", preflight, err)
	}
	restoreJob, replay, err := restores.Submit(ctx, preflight.Generation, backupResult.BackupID, backupdomain.RestoreActive, "RESTORE", "restore-command")
	if err != nil || replay {
		t.Fatalf("job=%#v replay=%v err=%v", restoreJob, replay, err)
	}
	restoreJob, err = restores.Execute(ctx, restoreJob.ID)
	if err != nil || restoreJob.Status != "succeeded" || restoreJob.Result == nil {
		t.Fatalf("job=%#v err=%v", restoreJob, err)
	}
	reopenedHandle, active := manager.ActiveHandle()
	if !active {
		t.Fatal("project was not reopened")
	}
	reopened := reopenedHandle.(interface{ Store() *store.Store }).Store()
	restored, err := reopened.Get(ctx, domain.KindTag, entity.ID)
	if err != nil || restored.Name != "Before" || reopened.ProjectID() != info.ID {
		t.Fatalf("restored=%#v project=%s err=%v", restored, reopened.ProjectID(), err)
	}
	if restoredRevision, revisionErr := reopened.GetRevisionRecord(ctx, checkpoint.ID); revisionErr != nil || restoredRevision.Metadata.RevisionID != checkpoint.ID || restoredRevision.Metadata.ConfigHash != checkpoint.ConfigHash {
		t.Fatalf("restored revision=%#v err=%v", restoredRevision, revisionErr)
	}
	if restoredRelease, releaseErr := reopened.GetRelease(ctx, release.ID); releaseErr != nil || restoredRelease.ID != release.ID || restoredRelease.RevisionID != checkpoint.ID {
		t.Fatalf("restored release=%#v err=%v", restoredRelease, releaseErr)
	}
	if restoredReport, reportErr := reopened.GetValidationReport(ctx, string(validationReport.Run.ID)); reportErr != nil || restoredReport.Run.ID != validationReport.Run.ID || restoredReport.Run.ResultHash != validationReport.Run.ResultHash {
		t.Fatalf("restored report=%#v err=%v", restoredReport, reportErr)
	}
	if restoredAuditJob, auditErr := reopened.GetJob(ctx, auditJob.ID); auditErr != nil || restoredAuditJob.RequestHash != auditJob.RequestHash {
		t.Fatalf("restored audit job=%#v err=%v", restoredAuditJob, auditErr)
	}
	if restoredAuditEvents, auditErr := reopened.ListEvents(ctx, auditJob.ID, 0); auditErr != nil || len(restoredAuditEvents) != 1 || restoredAuditEvents[0].Phase != "recorded" {
		t.Fatalf("restored audit events=%#v err=%v", restoredAuditEvents, auditErr)
	}
	graphState, found, err := reopened.GetGraphSyncState(ctx, checkpoint.ID)
	if err != nil || !found || graphState.Pipeline != graphsync.StateQueued || graphState.Generation != 3 || len(graphState.Warnings) != 1 || graphState.Warnings[0] != "RESTORE_PENDING_VERIFICATION" {
		t.Fatalf("restored graph state=%#v found=%v err=%v", graphState, found, err)
	}
	durableJob, err := reopened.GetJob(ctx, restoreJob.ID)
	if err != nil || durableJob.Status != "succeeded" || durableJob.RequestHash != restoreJob.RequestHash {
		t.Fatalf("durable job=%#v err=%v", durableJob, err)
	}
	events, err := reopened.ListEvents(ctx, restoreJob.ID, 0)
	if err != nil || len(events) < 2 {
		t.Fatalf("restore events=%#v err=%v", events, err)
	}
	for index := 1; index < len(events); index++ {
		if events[index].Ordinal <= events[index-1].Ordinal {
			t.Fatalf("restore event ordinals are not monotonic: %#v", events)
		}
	}
	lastEvent := events[len(events)-1]
	if lastEvent.Phase != "succeeded" || lastEvent.Result == nil || lastEvent.Result.ID != restoreJob.ID || lastEvent.Result.URL != "/api/v1/restores/"+string(restoreJob.ID) {
		t.Fatalf("terminal restore event=%#v", lastEvent)
	}
	database, err := sql.Open("sqlite", filepath.Join(projectDirectory, "project.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var restorePreJSON []byte
	if err = database.QueryRowContext(ctx, `SELECT result_json FROM backup_artifact_audit WHERE job_id=? AND backup_type='restore-pre'`, restoreJob.ID).Scan(&restorePreJSON); err != nil {
		t.Fatal(err)
	}
	restorePreResult, err := backupdomain.DecodeResult(restorePreJSON)
	if err != nil || restorePreResult.Source.CallerJobID != restoreJob.ID || restorePreResult.Source.RequestHash != restoreJob.RequestHash {
		t.Fatalf("restore-pre result=%#v err=%v", restorePreResult, err)
	}
	var reconciliationRows, invalidationRows int
	if err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM restore_reconciliation WHERE job_id=? AND request_hash=?`, restoreJob.ID, restoreJob.RequestHash).Scan(&reconciliationRows); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM restore_graph_invalidations WHERE job_id=? AND request_hash=?`, restoreJob.ID, restoreJob.RequestHash).Scan(&invalidationRows); err != nil {
		t.Fatal(err)
	}
	if reconciliationRows != 1 || invalidationRows != 1 {
		t.Fatalf("reconciliation=%d graph invalidation=%d", reconciliationRows, invalidationRows)
	}
	if _, found, err := journalStore.Load(ctx, restoreJob.ID); err != nil || found {
		t.Fatalf("terminal journal found=%v err=%v", found, err)
	}
	if err = manager.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
