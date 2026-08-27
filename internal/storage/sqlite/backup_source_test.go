package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestOnlineBackupProducesIndependentVerifiedSnapshotWithMonotonicProgress(t *testing.T) {
	store := newStore(t)
	for index := 0; index < 25; index++ {
		if _, _, err := store.Create(context.Background(), domain.KindTag, tagDraft(fmt.Sprintf("seed_%d", index))); err != nil {
			t.Fatal(err)
		}
	}
	source := BackupSource{Store: store, AppVersion: "test", StepPages: 1}
	identity, err := source.Identity(context.Background())
	if err != nil || identity.ProjectID != store.ProjectID() || identity.SchemaVersion != DBSchemaVersion() {
		t.Fatalf("identity=%#v err=%v", identity, err)
	}
	destination := filepath.Join(t.TempDir(), databaseName)
	lastPages, calls := int64(-1), 0
	err = source.OnlineBackup(context.Background(), destination, func(progress ports.BackupProgress) error {
		if progress.CopiedPages < lastPages || progress.TotalPages < progress.CopiedPages || progress.CopiedBytes < 0 {
			t.Fatalf("non-monotonic progress: %#v after %d", progress, lastPages)
		}
		lastPages, calls = progress.CopiedPages, calls+1
		return nil
	})
	if err != nil || calls == 0 {
		t.Fatalf("backup err=%v progress calls=%d", err, calls)
	}
	verified, err := VerifyBackupFile(context.Background(), destination, identity.ProjectID, identity.SchemaVersion)
	if err != nil || verified.Bytes < 1 || len(verified.SHA256) != 64 {
		t.Fatalf("verified=%#v err=%v", verified, err)
	}
	info, err := os.Stat(destination)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode=%v err=%v", info.Mode().Perm(), err)
	}
}

func TestOnlineBackupRemainsConsistentDuringSerializedWALWrites(t *testing.T) {
	store := newStore(t)
	source := BackupSource{Store: store, AppVersion: "test", StepPages: 1, MaxBusy: 4}
	identity, err := source.Identity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	operationErrors := make(chan error, 2)
	writers.Add(2)
	go func() {
		defer writers.Done()
		for index := 0; index < 250; index++ {
			if _, _, writeErr := store.Create(context.Background(), domain.KindTag, tagDraft(fmt.Sprintf("writer_%d", index))); writeErr != nil {
				operationErrors <- writeErr
				return
			}
		}
	}()
	go func() {
		defer writers.Done()
		for index := 0; index < 500; index++ {
			if _, readErr := store.List(context.Background(), domain.KindTag, "", "", 10); readErr != nil {
				operationErrors <- readErr
				return
			}
		}
	}()
	destination := filepath.Join(t.TempDir(), databaseName)
	if err = source.OnlineBackup(context.Background(), destination, nil); err != nil {
		t.Fatal(err)
	}
	writers.Wait()
	close(operationErrors)
	for operationErr := range operationErrors {
		t.Fatalf("ordinary database operation failed during backup: %v", operationErr)
	}
	if _, err = VerifyBackupFile(context.Background(), destination, identity.ProjectID, identity.SchemaVersion); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Create(context.Background(), domain.KindTag, tagDraft("after_high_churn_backup")); err != nil {
		t.Fatalf("ordinary write unavailable after backup: %v", err)
	}
}

func TestOnlineBackupHonorsCancellationWithoutChangingActiveDatabase(t *testing.T) {
	store := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	destination := filepath.Join(t.TempDir(), databaseName)
	if err := (BackupSource{Store: store, AppVersion: "test"}).OnlineBackup(ctx, destination, nil); err == nil {
		t.Fatal("canceled backup succeeded")
	}
	if _, _, err := store.Create(context.Background(), domain.KindTag, tagDraft("still_usable")); err != nil {
		t.Fatalf("active database was damaged: %v", err)
	}
}

func TestTwoGiBSparseFixtureHashesWithBoundedMemoryAndCancellation(t *testing.T) {
	const fixtureBytes int64 = 2 * 1024 * 1024 * 1024
	path := filepath.Join(t.TempDir(), "two-gib-sparse.bin")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(fixtureBytes); err != nil {
		_ = file.Close()
		t.Skipf("2 GiB sparse files are unavailable: %v", err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	bytesRead, hash, err := hashRegularFile(context.Background(), path, fixtureBytes)
	runtime.ReadMemStats(&after)
	if err != nil || bytesRead != fixtureBytes || len(hash) != 64 {
		t.Fatalf("bytes=%d hash=%q err=%v", bytesRead, hash, err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated > 16*1024*1024 {
		t.Fatalf("2 GiB stream allocated %d bytes, want <=16 MiB", allocated)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err = hashRegularFile(canceled, path, fixtureBytes); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled hash err=%v", err)
	}
	t.Logf("2 GiB streamed hash allocated=%d elapsed=%s", allocated, time.Since(started))
}
