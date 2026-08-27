package integration_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	root "github.com/zouyi/eco-guardian"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	backupintegration "github.com/zouyi/eco-guardian/internal/backup/integration"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	_ "modernc.org/sqlite"
)

type fixedRoots struct{ base string }

func (roots fixedRoots) ResolveProjectRoot(_ context.Context, id domain.ID) (string, error) {
	return filepath.Join(roots.base, string(id)), nil
}
func (fixedRoots) ApplyNativeSelection(context.Context, string, string) error { return nil }
func (fixedRoots) ResetDefault(context.Context) error                         { return nil }

func TestPopulatedV1MigrationPublishesVerifiedArtifactBeforeSchemaWrites(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	projectDirectory := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(projectDirectory, "project.db"))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := root.Assets.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(initial)); err != nil {
		t.Fatal(err)
	}
	projectID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	revisionID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = db.Exec(`INSERT INTO project_meta(id,db_schema_version,created_at) VALUES(?,?,?); INSERT INTO config_revisions(id,display_revision,config_hash,created_at) VALUES(?,?,?,?)`, projectID, 1, now, revisionID, 1, strings.Repeat("a", 64), now); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	backupBase := filepath.Clean(t.TempDir())
	adapter := backupintegration.MigrationBackup{Roots: fixedRoots{base: backupBase}, AppVersion: "test", Space: backupfs.Probe{}}
	opened, openedID, err := store.OpenWithMigrationBackup(context.Background(), projectDirectory, registry, adapter)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if openedID != projectID {
		t.Fatalf("opened project=%s want=%s", openedID, projectID)
	}
	artifacts, err := backupfs.NewStore(backupBase, store.DBSchemaVersion())
	if err != nil {
		t.Fatal(err)
	}
	page, err := artifacts.List(context.Background(), ports.InventoryQuery{ProjectID: projectID})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("inventory=%#v err=%v", page, err)
	}
	artifact := page.Items[0]
	if artifact.Type != backupdomain.Migration || artifact.SchemaVersion != 1 || artifact.Validation != backupdomain.ValidationValid || artifact.Compatibility != backupdomain.CompatibilityOlder || artifact.Source.MigrationID != "schema-1-to-22" {
		t.Fatalf("migration artifact=%#v", artifact)
	}
}
