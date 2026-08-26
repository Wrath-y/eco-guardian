package runtime

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	_ "modernc.org/sqlite"
)

func TestMachineStateStaysOutsideProjectAndProjectBackup(t *testing.T) {
	ctx := context.Background()
	machineRoot := filepath.Join(t.TempDir(), "machine", "EcoGuardian")
	projectRoot := filepath.Join(t.TempDir(), "project")
	settingsPath := filepath.Join(machineRoot, "settings.json")
	settingsStore := runtimeconfig.NewStore(settingsPath)
	settings := runtimeconfig.Default()
	settings.AI.Model = "machine-model-canary"
	if err := settingsStore.Save(settings); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(machineRoot, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(machineRoot, "runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(machineRoot, "logs", "eco.jsonl"), []byte("machine-log-canary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(machineRoot, "runtime", "process.json"), []byte("machine-runtime-canary"), 0o600); err != nil {
		t.Fatal(err)
	}

	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	projectStore, projectID, err := store.Create(ctx, projectRoot, registry)
	if err != nil {
		t.Fatal(err)
	}
	entity, revision, err := projectStore.Create(ctx, domain.KindTag, domain.EntityDraft{
		Key: "project_fact_canary", Name: "Project fact",
		Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := projectStore.CreateOrGetGraphJob(ctx, graphsync.GraphJobRequest{
		ProjectID: projectID, RevisionID: revision.ID, InputHash: revision.ConfigHash,
		IdempotencyKey: "project-job-canary", RequestHash: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	recent := project.NewFileRecentProjects(filepath.Dir(machineRoot))
	if err := recent.Record(project.ProjectInfo{ID: projectID, Name: "Recent project", Path: projectRoot}); err != nil {
		t.Fatal(err)
	}
	if err := projectStore.Close(); err != nil {
		t.Fatal(err)
	}

	for _, machineFile := range []string{settingsPath, filepath.Join(filepath.Dir(machineRoot), "EcoGuardian", "recent-projects.json"), filepath.Join(machineRoot, "logs", "eco.jsonl"), filepath.Join(machineRoot, "runtime", "process.json")} {
		if _, err := os.Stat(machineFile); err != nil {
			t.Fatalf("machine state missing outside project: %s: %v", machineFile, err)
		}
	}
	if _, err := os.Stat(filepath.Join(machineRoot, "project.db")); !os.IsNotExist(err) {
		t.Fatalf("machine directory unexpectedly contains project.db: %v", err)
	}

	projectDB := filepath.Join(projectRoot, "project.db")
	db, err := sql.Open("sqlite", projectDB)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for table, want := range map[string]int{"working_entities": 1, "config_revisions": 1, "jobs": 1} {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != want {
			t.Fatalf("project fact table %s count=%d want=%d err=%v", table, count, want, err)
		}
	}
	for _, forbiddenTable := range []string{"settings", "recent_projects", "runtime_metadata", "logs"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, forbiddenTable).Scan(&count); err != nil || count != 0 {
			t.Fatalf("machine table %s entered project.db: count=%d err=%v", forbiddenTable, count, err)
		}
	}
	backupPath := filepath.Join(t.TempDir(), "project-backup.db")
	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, backupPath); err != nil {
		t.Fatal(err)
	}
	backupBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{[]byte("machine-model-canary"), []byte("machine-log-canary"), []byte("machine-runtime-canary"), []byte(projectRoot)} {
		if bytes.Contains(backupBytes, forbidden) {
			t.Fatalf("project backup contains machine state %q", forbidden)
		}
	}
	backup, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var entityID, jobID string
	if err := backup.QueryRowContext(ctx, `SELECT id FROM working_entities WHERE id=?`, entity.ID).Scan(&entityID); err != nil || entityID != string(entity.ID) {
		t.Fatalf("backup lost entity fact: id=%q err=%v", entityID, err)
	}
	if err := backup.QueryRowContext(ctx, `SELECT id FROM jobs WHERE id=?`, job.ID).Scan(&jobID); err != nil || jobID != string(job.ID) {
		t.Fatalf("backup lost project Job: id=%q err=%v", jobID, err)
	}
}
