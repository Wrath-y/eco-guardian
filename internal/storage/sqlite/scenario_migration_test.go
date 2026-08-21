package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	root "github.com/zouyi/eco-guardian"
	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestV10ProjectUpgradeRequiresBackupAndSeedsBuiltinsOnce(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, databaseName))
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
	projectID, revisionID := mustID(t), mustID(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = db.Exec(`INSERT INTO project_meta(id,db_schema_version,created_at) VALUES(?,?,?)`, projectID, 1, now); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range []func(context.Context, *sql.Tx) error{applyMigrationV2, applyMigrationV3, applyMigrationV4, applyMigrationV5, applyMigrationV6, applyMigrationV7, applyMigrationV8, applyMigrationV9, applyMigrationV10} {
		if err = migration(context.Background(), tx); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO config_revisions(id,display_revision,config_hash,created_at) VALUES(?,?,?,?)`, revisionID, 1, strings.Repeat("a", 64), now); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = OpenWithMigrationBackup(context.Background(), dir, registry, nil); !errors.Is(err, ErrMigrationBackupRequired) {
		t.Fatalf("missing backup err=%v", err)
	}
	backup := &fakeMigrationBackup{evidence: BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("b", 64)}}
	store, id, err := OpenWithMigrationBackup(context.Background(), dir, registry, backup)
	if err != nil {
		t.Fatal(err)
	}
	if id != projectID || backup.calls != 1 {
		t.Fatalf("project=%s backup calls=%d", id, backup.calls)
	}
	definitions, err := store.ListScenarioDefinitions(context.Background())
	if err != nil || len(definitions) != 4 {
		t.Fatalf("definitions=%d err=%v", len(definitions), err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := Open(dir, registry)
	if err != nil {
		t.Fatal(err)
	}
	deferred, err := reopened.ListScenarioDefinitions(context.Background())
	_ = reopened.Close()
	if err != nil || len(deferred) != 4 {
		t.Fatalf("reopened definitions=%d err=%v", len(deferred), err)
	}
}
