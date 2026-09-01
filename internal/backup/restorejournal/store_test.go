package restorejournal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

func journalFixture(t *testing.T, root string) ports.RestoreJournal {
	t.Helper()
	jobID, _ := domain.NewID()
	projectID, _ := domain.NewID()
	backupID, _ := domain.NewID()
	now := time.Now().UTC()
	job := sharedjob.Record{ID: jobID, ProjectID: projectID, Kind: "restore", InputHash: strings.Repeat("a", 64), IdempotencyKey: "restore-key", RequestHash: strings.Repeat("a", 64), Status: sharedjob.Queued, CreatedAt: now, UpdatedAt: now}
	return ports.RestoreJournal{Version: backupdomain.RestoreJournalVersion, Job: job, Generation: 1, Phase: backupdomain.RestorePreflighted, ProjectID: projectID, BackupID: backupID, ManifestHash: strings.Repeat("b", 64), DatabaseHash: strings.Repeat("c", 64), DatabaseBytes: 4096, SchemaVersion: 22, CommandHash: strings.Repeat("a", 64), PreflightGeneration: strings.Repeat("d", 64), TargetMode: backupdomain.RestoreActive, TargetPath: filepath.Join(root, "project"), TargetIdentity: strings.Repeat("e", 64), TargetGeneration: strings.Repeat("f", 64)}
}

func TestJournalStrictCASFlushReplaceAndTerminalCleanup(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	journal := journalFixture(t, root)
	if changed, err := store.CompareAndSwap(context.Background(), 0, journal); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	loaded, found, err := store.Load(context.Background(), journal.Job.ID)
	if err != nil || !found || loaded.Generation != 1 || loaded.Phase != backupdomain.RestorePreflighted {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}
	if err := validateRestrictedPath(filepath.Join(root, string(journal.Job.ID)+".json"), false); err != nil {
		t.Fatalf("journal permissions err=%v", err)
	}
	next := journal
	next.Generation = 2
	next.Phase = backupdomain.RestoreMaintenance
	if changed, err := store.CompareAndSwap(context.Background(), 1, next); err != nil || !changed {
		t.Fatalf("advance changed=%v err=%v", changed, err)
	}
	if _, err := store.CompareAndSwap(context.Background(), 1, next); !errors.Is(err, ErrJournalInvalid) && !errors.Is(err, ErrJournalConflict) {
		t.Fatalf("stale CAS err=%v", err)
	}
	terminal := next
	terminal.Generation = 3
	terminal.Phase = backupdomain.RestoreRolledBack
	if _, err := store.CompareAndSwap(context.Background(), 2, terminal); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteTerminal(context.Background(), terminal.Job.ID, terminal.Generation); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Load(context.Background(), terminal.Job.ID); err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestJournalRejectsUnknownFieldsSymlinksAndSkippedPhases(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	store, _ := New(root)
	journal := journalFixture(t, root)
	if _, err := store.CompareAndSwap(context.Background(), 0, journal); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, string(journal.Job.ID)+".json")
	encoded, _ := os.ReadFile(path)
	corrupt := append(encoded[:len(encoded)-1], []byte(`,"unknown":true}`)...)
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Load(context.Background(), journal.Job.ID); !errors.Is(err, ErrJournalInvalid) {
		t.Fatalf("unknown field err=%v", err)
	}
	_ = os.Remove(path)
	if err := os.Symlink(filepath.Join(root, "missing"), path); err == nil {
		if _, _, loadErr := store.Load(context.Background(), journal.Job.ID); !errors.Is(loadErr, ErrJournalInvalid) {
			t.Fatalf("symlink err=%v", loadErr)
		}
	}
}
