package rootconfig

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
)

type selector string

func (value selector) SelectDirectory(context.Context) (string, error) { return string(value), nil }

func TestNativeSelectionUpdatesSingleSettingsOwnerAndRejectsOverlap(t *testing.T) {
	settings := runtimeconfig.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	tokens := project.NewTokenStore(time.Minute, nil)
	defaultRoot := filepath.Join(t.TempDir(), "default")
	manager := &Manager{Settings: settings, Tokens: tokens, DefaultRoot: func() (string, error) { return defaultRoot, nil }, Probe: backupfs.Probe{}}
	active := filepath.Clean(t.TempDir())
	custom := filepath.Clean(t.TempDir())
	customCanonical, _ := filepath.EvalSymlinks(custom)
	token, _, err := manager.IssueSelection(context.Background(), selector(custom))
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.ApplyNativeSelection(context.Background(), token, active); err != nil {
		t.Fatal(err)
	}
	stored, _, err := settings.Load()
	if err != nil || stored.Backup.RootMode != runtimeconfig.BackupRootCustom || stored.Backup.RootPath != customCanonical {
		t.Fatalf("settings=%#v err=%v", stored.Backup, err)
	}
	projectID, _ := domain.NewID()
	root, err := manager.ResolveProjectRoot(context.Background(), projectID)
	if err != nil || root != filepath.Join(customCanonical, string(projectID)) {
		t.Fatalf("root=%q err=%v", root, err)
	}
	overlapToken, _, _ := manager.IssueSelection(context.Background(), selector(active))
	if err = manager.ApplyNativeSelection(context.Background(), overlapToken, active); !errors.Is(err, ErrRootOverlap) {
		t.Fatalf("overlap err=%v", err)
	}
	after, _, _ := settings.Load()
	if after.Backup.RootPath != customCanonical {
		t.Fatal("invalid selection changed prior root")
	}
}

func TestDefaultResetCreatesExternalManagedRootWithoutSecondSettingsFile(t *testing.T) {
	machine := t.TempDir()
	settings := runtimeconfig.NewStore(filepath.Join(machine, "settings.json"))
	root := filepath.Join(t.TempDir(), "Documents", "EcoGuardian Backups")
	manager := &Manager{Settings: settings, Tokens: project.NewTokenStore(time.Minute, nil), DefaultRoot: func() (string, error) { return root, nil }, Probe: backupfs.Probe{}}
	if err := manager.ResetDefault(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, _, err := settings.Load()
	if err != nil || stored.Backup.RootMode != runtimeconfig.BackupRootDefault || stored.Backup.RootPath != "" {
		t.Fatalf("settings=%#v err=%v", stored.Backup, err)
	}
}
