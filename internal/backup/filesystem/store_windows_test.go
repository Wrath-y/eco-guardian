//go:build windows

package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	"golang.org/x/sys/windows"
)

func TestWindowsLongPathCaseContainmentAndExactArtifactDeletion(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	for index := 0; len(root) < 320; index++ {
		root = filepath.Join(root, "long-backup-root-segment-"+strings.Repeat("x", 20))
	}
	store, err := NewStore(root, 22)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := domain.NewID()
	manifest, artifact := artifactFixture(t, store, projectID, backupdomain.Manual, time.Now().UTC(), []byte("windows-long-path"))
	if _, err = store.Publish(context.Background(), artifact, manifest); err != nil {
		t.Fatal(err)
	}
	caseVariantRoot := strings.ToUpper(store.root)
	caseVariantTarget := strings.ToLower(filepath.Join(store.root, string(projectID), "child"))
	if !contained(caseVariantRoot, caseVariantTarget) {
		t.Fatalf("Windows case-insensitive containment rejected %q in %q", caseVariantTarget, caseVariantRoot)
	}
	if err = store.TrashAndDelete(context.Background(), projectID, manifest.BackupID, manifest.ManifestHash); err != nil {
		t.Fatal(err)
	}
	page, err := store.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("exact target survived deletion: %#v err=%v", page.Items, err)
	}
}

func TestWindowsSharingViolationCannotEvictValidArtifact(t *testing.T) {
	store, err := NewStore(filepath.Clean(t.TempDir()), 22)
	if err != nil {
		t.Fatal(err)
	}
	projectID, _ := domain.NewID()
	manifest, artifact := artifactFixture(t, store, projectID, backupdomain.Daily, time.Now().UTC(), []byte("windows-sharing"))
	if _, err = store.Publish(context.Background(), artifact, manifest); err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Join(store.root, string(projectID))
	entries, err := os.ReadDir(projectRoot)
	if err != nil {
		t.Fatal(err)
	}
	var databasePath string
	for _, entry := range entries {
		if _, parseErr := parseFinalName(entry.Name()); parseErr == nil {
			databasePath = filepath.Join(projectRoot, entry.Name(), databaseFile)
			break
		}
	}
	if databasePath == "" {
		t.Fatal("published database was not found")
	}
	pointer, err := windows.UTF16PtrFromString(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.TrashAndDelete(context.Background(), projectID, manifest.BackupID, manifest.ManifestHash); err == nil {
		windows.CloseHandle(handle)
		t.Fatal("sharing violation unexpectedly evicted a valid artifact")
	}
	page, listErr := store.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
	if listErr != nil || len(page.Items) != 1 || page.Items[0].BackupID != manifest.BackupID {
		windows.CloseHandle(handle)
		t.Fatalf("artifact changed after sharing violation: %#v err=%v", page.Items, listErr)
	}
	if err = windows.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	if err = store.TrashAndDelete(context.Background(), projectID, manifest.BackupID, manifest.ManifestHash); err != nil {
		t.Fatal(err)
	}
}
