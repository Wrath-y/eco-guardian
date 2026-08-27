package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	modernsqlite "modernc.org/sqlite"
)

var (
	ErrOnlineBackupFailed = errors.New("sqlite online backup failed")
	ErrBackupIntegrity    = errors.New("sqlite backup integrity verification failed")
)

type onlineBackuper interface {
	NewBackup(string) (*modernsqlite.Backup, error)
}

// BackupSource is the sole Online Backup adapter over an already-active
// project Store. It borrows one managed source connection but never holds the
// serialized application write lane for the duration of the copy.
type BackupSource struct {
	Store      *Store
	AppVersion string
	StepPages  int32
	MaxBusy    int
}

var _ ports.ProjectSnapshotSource = BackupSource{}

func (source BackupSource) Identity(ctx context.Context) (ports.SnapshotIdentity, error) {
	if source.Store == nil || !source.Store.projectID.Valid() || source.AppVersion == "" {
		return ports.SnapshotIdentity{}, ErrOnlineBackupFailed
	}
	var schema int
	if err := source.Store.db.QueryRowContext(ctx, `SELECT db_schema_version FROM project_meta WHERE id=?`, source.Store.projectID).Scan(&schema); err != nil {
		return ports.SnapshotIdentity{}, err
	}
	return ports.SnapshotIdentity{ProjectID: source.Store.projectID, ProjectPath: filepath.Dir(source.Store.path), AppVersion: source.AppVersion, SchemaVersion: schema}, nil
}

func (source BackupSource) OnlineBackup(ctx context.Context, destination string, progress func(ports.BackupProgress) error) error {
	if source.Store == nil || destination == "" || filepath.Base(destination) != databaseName {
		return ErrOnlineBackupFailed
	}
	stepPages := source.StepPages
	if stepPages <= 0 {
		stepPages = 256
	}
	maxBusy := source.MaxBusy
	if maxBusy <= 0 {
		maxBusy = 12
	}
	var totalPages, pageSize int64
	if err := source.Store.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&totalPages); err != nil {
		return err
	}
	if err := source.Store.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return err
	}
	connection, err := source.Store.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	var copied int64
	err = connection.Raw(func(driverConnection any) error {
		backuper, ok := driverConnection.(onlineBackuper)
		if !ok {
			return ErrOnlineBackupFailed
		}
		backup, backupErr := backuper.NewBackup(destination)
		if backupErr != nil {
			return backupErr
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		busyAttempt := 0
		for more := true; more; {
			if contextErr := ctx.Err(); contextErr != nil {
				return contextErr
			}
			more, backupErr = backup.Step(stepPages)
			if backupErr != nil {
				if isSQLiteBusy(backupErr) && busyAttempt < maxBusy {
					busyAttempt++
					delay := time.Duration(1<<min(busyAttempt, 4)) * 10 * time.Millisecond
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(delay):
					}
					continue
				}
				return backupErr
			}
			busyAttempt = 0
			if more {
				copied += int64(stepPages)
				if copied > totalPages {
					copied = totalPages
				}
			} else {
				copied = totalPages
			}
			if progress != nil {
				if progressErr := progress(ports.BackupProgress{CopiedPages: copied, TotalPages: totalPages, CopiedBytes: copied * pageSize}); progressErr != nil {
					return progressErr
				}
			}
		}
		backupErr = backup.Finish()
		finished = true
		return backupErr
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOnlineBackupFailed, err)
	}
	if err = os.Chmod(destination, 0o600); err != nil {
		return fmt.Errorf("%w: %v", ErrOnlineBackupFailed, err)
	}
	return nil
}

func isSQLiteBusy(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "busy") || strings.Contains(message, "locked")
}

type VerifiedBackup struct {
	ProjectID     domain.ID
	SchemaVersion int
	Bytes         int64
	SHA256        string
}

type BackupVerifier struct{}

var _ ports.SnapshotVerifier = BackupVerifier{}

func (BackupVerifier) Verify(ctx context.Context, path string, expectedProject domain.ID, expectedSchema int) (ports.VerifiedSnapshot, error) {
	verified, err := VerifyBackupFile(ctx, path, expectedProject, expectedSchema)
	if err != nil {
		return ports.VerifiedSnapshot{}, err
	}
	return ports.VerifiedSnapshot{ProjectID: verified.ProjectID, SchemaVersion: verified.SchemaVersion, Bytes: verified.Bytes, SHA256: verified.SHA256}, nil
}

// VerifyBackupFile uses an independent connection and a streaming hash after
// the Online Backup destination is closed. It rejects any non-ok integrity
// row, identity drift, special file, or mutation during hashing.
func VerifyBackupFile(ctx context.Context, path string, expectedProject domain.ID, expectedSchema int) (VerifiedBackup, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return VerifiedBackup{}, ErrBackupIntegrity
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return VerifiedBackup{}, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()
	if _, err = db.ExecContext(ctx, `PRAGMA query_only=ON`); err != nil {
		return VerifiedBackup{}, err
	}
	rows, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return VerifiedBackup{}, err
	}
	ok := false
	for rows.Next() {
		var result string
		if err = rows.Scan(&result); err != nil {
			rows.Close()
			return VerifiedBackup{}, err
		}
		if result != "ok" || ok {
			rows.Close()
			return VerifiedBackup{}, ErrBackupIntegrity
		}
		ok = true
	}
	if err = rows.Close(); err != nil || !ok {
		return VerifiedBackup{}, ErrBackupIntegrity
	}
	foreignKeyRows, err := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return VerifiedBackup{}, err
	}
	if foreignKeyRows.Next() {
		foreignKeyRows.Close()
		return VerifiedBackup{}, ErrBackupIntegrity
	}
	if err = foreignKeyRows.Close(); err != nil {
		return VerifiedBackup{}, ErrBackupIntegrity
	}
	var projectID domain.ID
	var schema int
	if err = db.QueryRowContext(ctx, `SELECT id,db_schema_version FROM project_meta`).Scan(&projectID, &schema); err != nil || projectID != expectedProject || schema != expectedSchema {
		return VerifiedBackup{}, ErrBackupIntegrity
	}
	if err = db.Close(); err != nil {
		return VerifiedBackup{}, err
	}
	bytesWritten, hashValue, err := hashRegularFile(ctx, path, before.Size())
	if err != nil {
		return VerifiedBackup{}, ErrBackupIntegrity
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return VerifiedBackup{}, ErrBackupIntegrity
	}
	return VerifiedBackup{ProjectID: projectID, SchemaVersion: schema, Bytes: bytesWritten, SHA256: hashValue}, nil
}

func hashRegularFile(ctx context.Context, path string, expectedBytes int64) (int64, string, error) {
	if expectedBytes < 0 {
		return 0, "", ErrBackupIntegrity
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	digest := sha256.New()
	buffer := make([]byte, 256*1024)
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		var read int
		read, err = file.Read(buffer)
		if read > 0 {
			total += int64(read)
			if _, writeErr := digest.Write(buffer[:read]); writeErr != nil {
				err = writeErr
				break
			}
		}
		if errors.Is(err, io.EOF) {
			err = nil
			break
		}
		if err != nil {
			break
		}
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil || total != expectedBytes {
		return total, "", errors.Join(err, closeErr, ErrBackupIntegrity)
	}
	return total, fmt.Sprintf("%x", digest.Sum(nil)), nil
}
