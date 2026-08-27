package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	ErrBackupAuditInvalid  = errors.New("backup audit result is invalid")
	ErrBackupAuditConflict = errors.New("backup audit result conflicts with sealed evidence")
	ErrBackupAuditNotFound = errors.New("backup audit result not found")
)

var _ ports.BackupAuditStore = (*Store)(nil)

func (s *Store) SealBackupResult(ctx context.Context, jobID domain.ID, result backupdomain.Result) (bool, error) {
	if !result.Valid() || result.ProjectID != s.projectID || (jobID != "" && !jobID.Valid()) {
		return false, ErrBackupAuditInvalid
	}
	encoded, err := result.CanonicalJSON()
	if err != nil {
		return false, err
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	existing, scanErr := scanBackupResult(tx.QueryRowContext(ctx, `SELECT result_json FROM backup_artifact_audit WHERE backup_id=? AND project_uuid=?`, result.BackupID, s.projectID))
	if scanErr == nil {
		if existing != result {
			return false, ErrBackupAuditConflict
		}
		return true, tx.Commit()
	}
	if !errors.Is(scanErr, sql.ErrNoRows) {
		return false, scanErr
	}
	var nullableJob any
	if jobID.Valid() {
		nullableJob = string(jobID)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backup_artifact_audit(backup_id,project_uuid,job_id,backup_type,manifest_hash,database_hash,result_json,published_at) VALUES(?,?,?,?,?,?,?,?)`, result.BackupID, result.ProjectID, nullableJob, result.Type, result.ManifestHash, result.DBSHA256, encoded, result.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	return false, tx.Commit()
}

func (s *Store) GetBackupResult(ctx context.Context, backupID domain.ID) (backupdomain.Result, error) {
	if !backupID.Valid() {
		return backupdomain.Result{}, ErrBackupAuditNotFound
	}
	result, err := scanBackupResult(s.db.QueryRowContext(ctx, `SELECT result_json FROM backup_artifact_audit WHERE backup_id=? AND project_uuid=?`, backupID, s.projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return backupdomain.Result{}, ErrBackupAuditNotFound
	}
	return result, err
}

type backupResultScanner interface{ Scan(...any) error }

func scanBackupResult(scanner backupResultScanner) (backupdomain.Result, error) {
	var encoded []byte
	if err := scanner.Scan(&encoded); err != nil {
		return backupdomain.Result{}, err
	}
	result, err := backupdomain.DecodeResult(encoded)
	if err != nil {
		return backupdomain.Result{}, ErrBackupAuditInvalid
	}
	return result, nil
}
