package integration

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
)

func TestRecentProjectRegistryRequiresOneUseExactMigrationConfirmation(t *testing.T) {
	ctx := context.Background()
	projectID, _ := domain.NewID()
	records := project.NewFileRecentProjects(t.TempDir())
	current := filepath.Clean(t.TempDir())
	target := filepath.Clean(t.TempDir())
	if err := records.Record(project.ProjectInfo{ID: projectID, Name: "current", Path: current}); err != nil {
		t.Fatal(err)
	}
	registry := NewRecentProjectRegistry(records)
	resolved, found, err := registry.Resolve(ctx, projectID)
	if err != nil || !found || resolved.ProjectID != projectID || resolved.CanonicalPath == target || resolved.Generation < 1 {
		t.Fatalf("resolved=%#v found=%v err=%v", resolved, found, err)
	}
	token, expires, err := registry.IssueMigrationConfirmation(ctx, projectID, current, target)
	if err != nil || token == "" || !expires.After(time.Now()) {
		t.Fatalf("token=%q expires=%v err=%v", token, expires, err)
	}
	wrong := filepath.Clean(t.TempDir())
	if _, err = registry.ConfirmMigration(ctx, projectID, token, wrong); !errors.Is(err, ErrRegistryConfirmationInvalid) {
		t.Fatalf("wrong target err=%v", err)
	}
	if _, err = registry.ConfirmMigration(ctx, projectID, token, target); !errors.Is(err, ErrRegistryConfirmationInvalid) {
		t.Fatalf("consumed token was reusable: %v", err)
	}
	token, _, _ = registry.IssueMigrationConfirmation(ctx, projectID, current, target)
	updated, err := registry.ConfirmMigration(ctx, projectID, token, target)
	canonicalTarget, _ := canonicalRegisteredDirectory(target)
	if err != nil || updated.ProjectID != projectID || updated.CanonicalPath == resolved.CanonicalPath || updated.CanonicalPath != canonicalTarget {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	values, err := records.List()
	if err != nil || len(values) != 1 || values[0].ID != projectID || values[0].Path != current {
		t.Fatalf("records=%#v err=%v", values, err)
	}
}

func TestRecentProjectRegistryRejectsExpiredAndStaleConfirmationsWithoutMutation(t *testing.T) {
	ctx := context.Background()
	projectID, _ := domain.NewID()
	records := project.NewFileRecentProjects(t.TempDir())
	current := filepath.Clean(t.TempDir())
	target := filepath.Clean(t.TempDir())
	other := filepath.Clean(t.TempDir())
	if err := records.Record(project.ProjectInfo{ID: projectID, Name: "current", Path: current}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	registry := NewRecentProjectRegistry(records)
	registry.Clock = registryClockFunc(func() time.Time { return now })
	registry.TTL = time.Minute
	token, _, _ := registry.IssueMigrationConfirmation(ctx, projectID, current, target)
	now = now.Add(2 * time.Minute)
	if _, err := registry.ConfirmMigration(ctx, projectID, token, target); !errors.Is(err, ErrRegistryConfirmationInvalid) {
		t.Fatalf("expired confirmation err=%v", err)
	}
	now = now.Add(-time.Minute)
	token, _, _ = registry.IssueMigrationConfirmation(ctx, projectID, current, target)
	if err := records.Record(project.ProjectInfo{ID: projectID, Name: "other", Path: other}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ConfirmMigration(ctx, projectID, token, target); !errors.Is(err, ErrProjectRegistryConflict) {
		t.Fatalf("stale registry confirmation err=%v", err)
	}
	resolved, found, err := registry.Resolve(ctx, projectID)
	canonicalOther, _ := canonicalRegisteredDirectory(other)
	if err != nil || !found || resolved.CanonicalPath != canonicalOther {
		t.Fatalf("stale confirmation mutated registry: %#v found=%v err=%v", resolved, found, err)
	}
}
