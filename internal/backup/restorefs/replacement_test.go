package restorefs_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/backup/restorefs"
	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

func TestSameDirectoryReplacementRestoresExactSQLiteSnapshotWithoutWALMixing(t *testing.T) {
	ctx := context.Background()
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	projectDirectory := filepath.Clean(t.TempDir())
	project, _, err := store.Create(ctx, projectDirectory, registry)
	if err != nil {
		t.Fatal(err)
	}
	entity, _, err := project.Create(ctx, domain.KindTag, domain.EntityDraft{Key: "restore", Name: "Before", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}})
	if err != nil {
		t.Fatal(err)
	}
	artifactRoot := filepath.Clean(t.TempDir())
	artifacts, err := backupfs.NewStore(artifactRoot, store.DBSchemaVersion())
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store.BackupSource{Store: project, AppVersion: "test"}, artifacts, store.BackupVerifier{}, project, project, backupfs.Probe{})
	command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: project.ProjectID(), Purpose: backupdomain.Manual, ManualReason: "restore fixture", Source: backupdomain.SourceIdentity{}}
	job, _, err := service.Submit(ctx, command, "restore-fixture")
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ExecuteStored(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = project.Patch(ctx, domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": json.RawMessage(`"After"`)}); err != nil {
		t.Fatal(err)
	}
	projectID := project.ProjectID()
	if err = project.Close(); err != nil {
		t.Fatal(err)
	}
	record, lease, err := artifacts.Acquire(ctx, projectID, result.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	replacement := restorefs.Replacement{Verifier: store.BackupVerifier{}}
	staged, err := replacement.Stage(ctx, lease.DatabasePath(), projectDirectory, job.ID, projectID, record.SchemaVersion, record.DBBytes, record.DBSHA256)
	if err != nil {
		t.Fatal(err)
	}
	original, err := replacement.ParkOriginal(ctx, projectDirectory, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = replacement.Install(ctx, projectDirectory, job.ID, staged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = replacement.VerifyInstalled(ctx, projectDirectory, projectID, record.SchemaVersion, record.DBBytes, record.DBSHA256); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := store.Open(projectDirectory, registry)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := reopened.Get(ctx, domain.KindTag, entity.ID)
	if err != nil || restored.Name != "Before" {
		t.Fatalf("restored=%#v err=%v", restored, err)
	}
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if err = replacement.Cleanup(ctx, projectDirectory, job.ID, original); err != nil {
		t.Fatal(err)
	}
}

var _ ports.DatabaseReplacement = restorefs.Replacement{}

func TestInstallRejectsStagedPathSwapAndPreservesRollbackCopy(t *testing.T) {
	ctx := context.Background()
	target, err := filepath.EvalSymlinks(filepath.Clean(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	jobID, _ := domain.NewID()
	originalBytes := []byte("original-database")
	stagedBytes := []byte("verified-database")
	mainPath := filepath.Join(target, "project.db")
	originalPath := filepath.Join(target, ".eco-restore-"+string(jobID)+".original")
	stagedPath := filepath.Join(target, ".eco-restore-"+string(jobID)+".staging")
	if err := os.WriteFile(mainPath, originalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(mainPath, originalPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stagedPath, stagedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	originalDigest := sha256.Sum256(originalBytes)
	stagedDigest := sha256.Sum256(stagedBytes)
	original := ports.RestoreFileEvidence{Path: originalPath, Bytes: int64(len(originalBytes)), SHA256: fmt.Sprintf("%x", originalDigest)}
	staged := ports.RestoreFileEvidence{Path: stagedPath, Bytes: int64(len(stagedBytes)), SHA256: fmt.Sprintf("%x", stagedDigest)}

	// Preserve the same length so Install must re-hash the bytes rather than
	// relying on the earlier Stage evidence or a size-only check.
	if err := os.WriteFile(stagedPath, []byte("attacker-database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (restorefs.Replacement{}).Install(ctx, target, jobID, staged); !errors.Is(err, restorefs.ErrReplacementConflict) {
		t.Fatalf("path-swap install err=%v", err)
	}
	if _, err := os.Stat(originalPath); err != nil {
		t.Fatalf("rollback copy was removed: %v", err)
	}
	if _, err := os.Stat(stagedPath); err != nil {
		t.Fatalf("changed staged input was removed or renamed: %v", err)
	}
	if _, err := os.Stat(mainPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("changed staged input reached project.db: %v", err)
	}
	if err := (restorefs.Replacement{}).Rollback(ctx, target, jobID, original); err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(mainPath)
	if err != nil || string(restored) != string(originalBytes) {
		t.Fatalf("restored=%q err=%v", restored, err)
	}
}
