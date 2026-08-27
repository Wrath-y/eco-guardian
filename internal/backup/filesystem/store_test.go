package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
)

func artifactFixture(t *testing.T, store *Store, projectID domain.ID, kind backupdomain.Type, created time.Time, contents []byte) (backupdomain.Manifest, ports.StagingArtifact) {
	t.Helper()
	backupID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.CreateStaging(context.Background(), projectID, backupID)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(artifact.DatabasePath(), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	trigger := backupdomain.TriggerUser
	switch kind {
	case backupdomain.Daily:
		trigger = backupdomain.TriggerPolicy
	case backupdomain.Release:
		trigger = backupdomain.TriggerRelease
	case backupdomain.Migration:
		trigger = backupdomain.TriggerMigration
	case backupdomain.RestorePre:
		trigger = backupdomain.TriggerRestore
	}
	manifest, err := backupdomain.NewManifest(backupdomain.ManifestBody{ManifestVersion: backupdomain.ManifestSchemaVersion, BackupID: backupID, ProjectID: projectID, Type: kind, Trigger: trigger, CreatedAt: created.UTC(), AppVersion: "test", SchemaVersion: 21, DBBytes: int64(len(contents)), DBSHA256: fmt.Sprintf("%x", digest), Source: backupdomain.SourceIdentity{}, Extensions: map[string]json.RawMessage{}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := manifest.CanonicalJSON()
	if err = artifact.WriteManifest(context.Background(), encoded); err != nil {
		t.Fatal(err)
	}
	if err = artifact.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	return manifest, artifact
}

func TestArtifactPublicationInventoryLeaseAndExactTrashDeletion(t *testing.T) {
	store, err := NewStore(filepath.Clean(t.TempDir()), 21)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := domain.NewID()
	manifest, artifact := artifactFixture(t, store, projectID, backupdomain.Manual, time.Date(2026, 8, 26, 2, 0, 0, 0, time.UTC), []byte("fixture-database"))
	result, err := store.Publish(context.Background(), artifact, manifest)
	if err != nil || !result.Valid() {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	page, err := store.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
	if err != nil || len(page.Items) != 1 || !page.Items[0].Restorable() {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	projectRoot := filepath.Join(store.root, string(projectID))
	rootEntries, err := os.ReadDir(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	finalDirectories := make([]string, 0, 1)
	for _, entry := range rootEntries {
		if entry.IsDir() && entry.Name() != ".staging" && entry.Name() != ".trash" {
			finalDirectories = append(finalDirectories, entry.Name())
		}
	}
	if len(finalDirectories) != 1 {
		t.Fatalf("published package directories=%v", finalDirectories)
	}
	packageEntries, err := os.ReadDir(filepath.Join(projectRoot, finalDirectories[0]))
	if err != nil || len(packageEntries) != 2 || packageEntries[0].Name() != manifestFile || packageEntries[1].Name() != databaseFile {
		t.Fatalf("package entries=%v err=%v", packageEntries, err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(projectRoot, finalDirectories[0], manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"fixture-database", "credential", "settings", "local-rag", "embedding", "logs", "SELECT "} {
		if strings.Contains(string(manifestBytes), forbidden) {
			t.Fatalf("manifest leaked excluded content %q: %s", forbidden, manifestBytes)
		}
	}
	_, lease, err := store.Acquire(context.Background(), projectID, manifest.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.TrashAndDelete(context.Background(), projectID, manifest.BackupID, manifest.ManifestHash); !errors.Is(err, ErrArtifactPublished) {
		t.Fatalf("leased delete err=%v", err)
	}
	_ = lease.Release()
	if err = store.TrashAndDelete(context.Background(), projectID, manifest.BackupID, manifest.ManifestHash); err != nil {
		t.Fatal(err)
	}
	page, _ = store.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
	if len(page.Items) != 0 {
		t.Fatalf("deleted artifact still listed: %#v", page.Items)
	}
}

func TestArtifactStoreRejectsSymlinkHardLinkAndTraversalLookalikes(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	store, err := NewStore(root, 21)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := domain.NewID()
	manifest, artifact := artifactFixture(t, store, projectID, backupdomain.Manual, time.Now().UTC(), []byte("owned"))
	external := filepath.Join(t.TempDir(), "outside.db")
	if err = os.WriteFile(external, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(artifact.DatabasePath()); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(external, artifact.DatabasePath()); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err = store.Publish(context.Background(), artifact, manifest); !errors.Is(err, ErrPathSecurity) {
		t.Fatalf("symlink publish err=%v", err)
	}
	outside, _ := os.ReadFile(external)
	if string(outside) != "outside" {
		t.Fatal("outside target changed")
	}
	if _, err = store.CreateStaging(context.Background(), domain.ID("../escape"), manifest.BackupID); !errors.Is(err, ErrPathSecurity) {
		t.Fatalf("traversal project err=%v", err)
	}
}

func TestInventoryClassifiesChangedBytesAndIgnoresStagingLookalikes(t *testing.T) {
	store, err := NewStore(filepath.Clean(t.TempDir()), 21)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := domain.NewID()
	manifest, artifact := artifactFixture(t, store, projectID, backupdomain.Daily, time.Now().UTC(), []byte("before"))
	if _, err = store.Publish(context.Background(), artifact, manifest); err != nil {
		t.Fatal(err)
	}
	projectRoot, _ := store.projectRoot(projectID)
	entries, _ := os.ReadDir(projectRoot)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".ecobackup") {
			if err = os.WriteFile(filepath.Join(projectRoot, entry.Name(), databaseFile), []byte("after"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	page, err := store.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
	if err != nil || len(page.Items) != 1 || page.Items[0].Validation != backupdomain.ValidationDamaged || page.Items[0].Restorable() {
		t.Fatalf("page=%#v err=%v", page, err)
	}
}

func TestInventoryIgnoresAttackerLookalikeFilesAndDirectories(t *testing.T) {
	store, err := NewStore(filepath.Clean(t.TempDir()), 21)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := domain.NewID()
	manifest, artifact := artifactFixture(t, store, projectID, backupdomain.Manual, time.Now().UTC(), []byte("owned"))
	if _, err = store.Publish(context.Background(), artifact, manifest); err != nil {
		t.Fatal(err)
	}
	projectRoot, _ := store.projectRoot(projectID)
	lookalikes := []string{
		string(manifest.BackupID) + "_" + manifest.ManifestHash + ".ecobackup.tmp",
		"prefix_" + string(manifest.BackupID) + "_" + manifest.ManifestHash + ".ecobackup",
		string(manifest.BackupID) + "_" + strings.Repeat("f", 63) + ".ecobackup",
	}
	for index, name := range lookalikes {
		path := filepath.Join(projectRoot, name)
		if index%2 == 0 {
			if err = os.WriteFile(path, []byte("attacker"), 0o600); err != nil {
				t.Fatal(err)
			}
		} else if err = os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
	if err != nil || len(page.Items) != 1 || page.Items[0].BackupID != manifest.BackupID {
		t.Fatalf("lookalike inventory=%#v err=%v", page.Items, err)
	}
}

func FuzzFinalArtifactNameRejectsNonCanonicalPathComponents(f *testing.F) {
	valid := "20260826T030405.000000000Z_manual_018f0f3c-7b4a-7cc1-8f4b-1a2b3c4d5e6f_" + strings.Repeat("a", 64) + ".ecobackup"
	f.Add(valid)
	f.Add("../" + valid)
	f.Add(strings.Replace(valid, "manual", "manual/escape", 1))
	f.Add(strings.Replace(valid, strings.Repeat("a", 64), strings.Repeat("A", 64), 1))
	f.Fuzz(func(t *testing.T, name string) {
		parsed, err := parseFinalName(name)
		if err != nil {
			return
		}
		if strings.ContainsAny(name, "/\\\x00") || filepath.Base(name) != name {
			t.Fatalf("accepted path-bearing artifact name %q", name)
		}
		roundTrip := parsed.created.UTC().Format("20060102T150405.000000000Z") + "_" + string(parsed.kind) + "_" + string(parsed.id) + "_" + parsed.hash + ".ecobackup"
		if roundTrip != name {
			t.Fatalf("accepted non-canonical name %q round-trip %q", name, roundTrip)
		}
	})
}

func TestInventoryClassifiesIncompleteMissingNewerAndInvalidatesCachedBytes(t *testing.T) {
	store, err := NewStore(filepath.Clean(t.TempDir()), 20)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := domain.NewID()
	created := time.Date(2026, 8, 26, 3, 0, 0, 0, time.UTC)
	manifest, artifact := artifactFixture(t, store, projectID, backupdomain.Manual, created, []byte("cache-before"))
	if _, err = store.Publish(context.Background(), artifact, manifest); err != nil {
		t.Fatal(err)
	}
	page, err := store.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
	if err != nil || len(page.Items) != 1 || page.Items[0].Compatibility != backupdomain.CompatibilityNewer || page.Items[0].Validation != backupdomain.ValidationValid {
		t.Fatalf("initial inventory=%#v err=%v", page.Items, err)
	}
	projectRoot, _ := store.projectRoot(projectID)
	entries, _ := os.ReadDir(projectRoot)
	var finalRoot string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".ecobackup") {
			finalRoot = filepath.Join(projectRoot, entry.Name())
		}
	}
	if err = os.WriteFile(filepath.Join(finalRoot, databaseFile), []byte("cache-after!"), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err = store.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
	if err != nil || page.Items[0].Validation != backupdomain.ValidationDamaged {
		t.Fatalf("cache did not invalidate after byte change: %#v err=%v", page.Items, err)
	}
	if err = os.Remove(filepath.Join(finalRoot, databaseFile)); err != nil {
		t.Fatal(err)
	}
	page, err = store.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
	if err != nil || page.Items[0].Validation != backupdomain.ValidationMissing || page.Items[0].Restorable() {
		t.Fatalf("missing database classification=%#v err=%v", page.Items, err)
	}
	if err = os.Remove(filepath.Join(finalRoot, manifestFile)); err != nil {
		t.Fatal(err)
	}
	page, err = store.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
	if err != nil || page.Items[0].Validation != backupdomain.ValidationIncomplete || page.Items[0].Compatibility != backupdomain.CompatibilityUnknown || !page.Items[0].Valid() || page.Items[0].Restorable() {
		t.Fatalf("incomplete artifact classification=%#v err=%v", page.Items, err)
	}
}

func TestStartupCleanupRemovesOnlyOldStructurallyOwnedWork(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	store, err := NewStore(root, 21)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := domain.NewID()
	_, oldArtifact := artifactFixture(t, store, projectID, backupdomain.Manual, time.Now().UTC(), []byte("old staging"))
	oldPath := oldArtifact.(*staging).path
	oldTime := time.Now().Add(-48 * time.Hour)
	if err = os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	_, freshArtifact := artifactFixture(t, store, projectID, backupdomain.Manual, time.Now().UTC(), []byte("fresh staging"))
	freshPath := freshArtifact.(*staging).path
	projectRoot, _ := store.projectRoot(projectID)
	attackerID, _ := domain.NewID()
	attackerPath := filepath.Join(projectRoot, ".staging", string(attackerID))
	if err = os.Mkdir(attackerPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(attackerPath, "unexpected"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(attackerPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if _, err = NewStore(root, 21); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old owned staging was not removed: %v", err)
	}
	for _, preserved := range []string{freshPath, attackerPath} {
		if _, err = os.Stat(preserved); err != nil {
			t.Fatalf("preserved path %s: %v", filepath.Base(preserved), err)
		}
	}

	manifest, artifact := artifactFixture(t, store, projectID, backupdomain.Daily, time.Now().UTC(), []byte("trash artifact"))
	if _, err = store.Publish(context.Background(), artifact, manifest); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(projectRoot)
	var published string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".ecobackup") && strings.Contains(entry.Name(), string(manifest.BackupID)) {
			published = filepath.Join(projectRoot, entry.Name())
		}
	}
	trashID, _ := domain.NewID()
	trashPath := filepath.Join(projectRoot, ".trash", string(trashID)+"_"+string(manifest.BackupID)+"_"+manifest.ManifestHash)
	if err = os.Rename(published, trashPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(trashPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if _, err = NewStore(root, 21); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(trashPath); !os.IsNotExist(err) {
		t.Fatalf("old owned trash was not removed: %v", err)
	}
}
