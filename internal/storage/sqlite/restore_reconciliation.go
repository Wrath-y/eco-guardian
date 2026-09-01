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

// ReconcileRestoredJobs terminalizes nonterminal work captured in the selected
// backup timepoint. Such workers no longer exist after database replacement
// and must not be mistaken for resumable work or block project maintenance.
// The coordinating restore Job is excluded because it is completed by the
// restore service after all reconciliation succeeds.
func (s *Store) ReconcileRestoredJobs(ctx context.Context, coordinatorJobID domain.ID) error {
	if !coordinatorJobID.Valid() {
		return ErrRestoreReconciliationInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	type candidate struct {
		id     domain.ID
		status sharedjob.Status
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,status FROM jobs WHERE project_uuid=? AND id<>? AND status IN ('queued','running','interrupted') ORDER BY created_at,id`, s.projectID, coordinatorJobID)
	if err != nil {
		return err
	}
	candidates := []candidate{}
	for rows.Next() {
		var value candidate
		if err = rows.Scan(&value.id, &value.status); err != nil || !value.id.Valid() || !value.status.Valid() || value.status.Terminal() {
			_ = rows.Close()
			return ErrRestoreReconciliationConflict
		}
		candidates = append(candidates, value)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	for _, value := range candidates {
		var ordinal int64
		var progress int
		err = tx.QueryRowContext(ctx, `SELECT event_ordinal,progress FROM job_events WHERE job_id=? ORDER BY event_ordinal DESC LIMIT 1`, value.id).Scan(&ordinal, &progress)
		if errors.Is(err, sql.ErrNoRows) {
			ordinal, progress, err = 0, 0, nil
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO job_events(job_id,event_ordinal,phase,progress,error,created_at) VALUES(?,?,?,?,?,?)`, value.id, ordinal+1, "superseded_by_restore", progress, "JOB_SUPERSEDED_BY_RESTORE", now); err != nil {
			return err
		}
		updated, updateErr := tx.ExecContext(ctx, `UPDATE jobs SET status='failed',updated_at=? WHERE id=? AND project_uuid=? AND status=?`, now, value.id, s.projectID, value.status)
		if updateErr != nil {
			return updateErr
		}
		if affected, affectedErr := updated.RowsAffected(); affectedErr != nil || affected != 1 {
			return errors.Join(ErrRestoreReconciliationConflict, affectedErr)
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
