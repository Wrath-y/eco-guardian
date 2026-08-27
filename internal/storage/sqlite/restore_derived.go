package sqlite

import (
	"context"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrRestoreDerivedInvalid = errors.New("restore derived-state invalidation is invalid")

// InvalidateAfterRestore makes every restored Graph projection non-authoritative
// in one system transaction. Immutable revision and projection evidence stays
// untouched; only the derived readiness pointer is reset for re-verification.
func (s *Store) InvalidateAfterRestore(ctx context.Context, projectID, jobID domain.ID, requestHash string) error {
	if s == nil || projectID != s.projectID || !jobID.Valid() || !hash64(requestHash) {
		return ErrRestoreDerivedInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UTC().Format(time.RFC3339Nano)
	write, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO restore_graph_invalidations(job_id,project_uuid,request_hash,restore_generation,invalidated_at)
		SELECT job_id,project_uuid,request_hash,journal_generation,? FROM restore_reconciliation
		WHERE job_id=? AND project_uuid=? AND request_hash=?`, now, jobID, projectID, requestHash)
	if err != nil {
		return err
	}
	rows, err := write.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		var storedProject domain.ID
		var storedHash string
		var storedGeneration int64
		if err = tx.QueryRowContext(ctx, `SELECT project_uuid,request_hash,restore_generation FROM restore_graph_invalidations WHERE job_id=?`, jobID).Scan(&storedProject, &storedHash, &storedGeneration); err != nil || storedProject != projectID || storedHash != requestHash || storedGeneration < 1 {
			return errors.Join(ErrRestoreDerivedInvalid, err)
		}
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `UPDATE graph_sync_states
		SET pipeline_state='saved', latest_job_id=NULL, external_task_id=NULL,
			provider_request_id=NULL, provider_task_id=NULL, generation=generation+1,
			safe_error=NULL, warnings='["RESTORE_PENDING_VERIFICATION"]', updated_at=?`,
		now)
	if err != nil {
		return err
	}
	return tx.Commit()
}
