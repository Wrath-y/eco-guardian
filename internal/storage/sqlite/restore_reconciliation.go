package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var (
	ErrRestoreReconciliationInvalid  = errors.New("restore reconciliation is invalid")
	ErrRestoreReconciliationConflict = errors.New("restore reconciliation conflicts with restored database")
)

var _ ports.RestoreReconciliationStore = (*Store)(nil)

// ReconcileRestoreBackup restores the mandatory restore-pre audit fact that
// was committed after the selected backup timepoint. The artifact remains
// filesystem-derived; this only seals its canonical evidence against the
// original restore Job in the newly installed database.
func (s *Store) ReconcileRestoreBackup(ctx context.Context, jobID domain.ID, result backupdomain.Result) error {
	if !jobID.Valid() || !result.Valid() || result.ProjectID != s.projectID || result.Type != backupdomain.RestorePre || result.Source.CallerJobID != jobID {
		return ErrRestoreReconciliationInvalid
	}
	_, err := s.SealBackupResult(ctx, jobID, result)
	return err
}

// ReconcileRestoreJob preserves the original restore Job identity after the
// selected database timepoint is installed. Missing pre-replacement events are
// represented by the journal ordinal; they are never fabricated or renumbered.
func (s *Store) ReconcileRestoreJob(ctx context.Context, original sharedjob.Record, lastOrdinal, journalGeneration int64) error {
	if !original.Valid() || original.Kind != "restore" || original.ProjectID != s.projectID || lastOrdinal < 0 || journalGeneration < 1 {
		return ErrRestoreReconciliationInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	existing, scanErr := scanSharedJob(tx.QueryRowContext(ctx, sharedJobSelect+` WHERE id=? AND project_uuid=?`, original.ID, s.projectID))
	if scanErr == nil {
		if !existing.Request().Equivalent(original.Request()) || existing.Kind != "restore" {
			return ErrRestoreReconciliationConflict
		}
	} else if !errors.Is(scanErr, sql.ErrNoRows) {
		return scanErr
	} else {
		now := s.now().UTC()
		status := sharedjob.Interrupted
		_, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,status,cancel_generation,cancel_requested_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, original.ID, original.ProjectID, original.Kind, nullableID(original.RevisionID), original.InputHash, original.IdempotencyKey, original.RequestHash, status, 0, nil, original.CreatedAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
	}
	write, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO restore_reconciliation(job_id,project_uuid,request_hash,idempotency_key,last_event_ordinal,journal_generation,reconciled_at) VALUES(?,?,?,?,?,?,?)`, original.ID, original.ProjectID, original.RequestHash, original.IdempotencyKey, lastOrdinal, journalGeneration, s.now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	rows, err := write.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		var requestHash, key string
		var ordinal, generation int64
		if err = tx.QueryRowContext(ctx, `SELECT request_hash,idempotency_key,last_event_ordinal,journal_generation FROM restore_reconciliation WHERE job_id=? AND project_uuid=?`, original.ID, original.ProjectID).Scan(&requestHash, &key, &ordinal, &generation); err != nil {
			return err
		}
		if requestHash != original.RequestHash || key != original.IdempotencyKey || ordinal > lastOrdinal || generation > journalGeneration {
			return ErrRestoreReconciliationConflict
		}
		if ordinal != lastOrdinal || generation != journalGeneration {
			if _, err = tx.ExecContext(ctx, `UPDATE restore_reconciliation SET last_event_ordinal=?,journal_generation=?,reconciled_at=? WHERE job_id=? AND project_uuid=?`, lastOrdinal, journalGeneration, s.now().UTC().Format(time.RFC3339Nano), original.ID, original.ProjectID); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) VerifyRestoreRuntime(ctx context.Context) error {
	connection, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	var journalMode string
	var foreignKeys, busyTimeout int
	if err = connection.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		return err
	}
	if err = connection.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		return err
	}
	if err = connection.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		return err
	}
	if journalMode != "wal" || foreignKeys != 1 || busyTimeout < 5000 {
		return ErrRestoreReconciliationInvalid
	}
	return nil
}
