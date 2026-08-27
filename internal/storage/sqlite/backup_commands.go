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
	ErrBackupCommandInvalid  = errors.New("backup command is invalid")
	ErrBackupCommandNotFound = errors.New("backup command not found")
)

var _ ports.BackupCommandStore = (*Store)(nil)
var _ ports.BackupPublicationStore = (*Store)(nil)

// AdmitBackup commits the shared Job and immutable command in one transaction.
// The existing project-scoped idempotency index remains the sole admission
// authority used by all Job kinds.
func (s *Store) AdmitBackup(ctx context.Context, command backupdomain.Command, idempotencyKey string) (sharedjob.Record, bool, error) {
	encoded, err := command.CanonicalJSON()
	if err != nil || idempotencyKey == "" || command.ProjectID != s.projectID {
		return sharedjob.Record{}, false, ErrBackupCommandInvalid
	}
	hash, err := command.Hash()
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	request := sharedjob.Request{ProjectID: command.ProjectID, Kind: "backup", RevisionID: command.Source.RevisionID, InputHash: hash, IdempotencyKey: idempotencyKey, RequestHash: hash}
	if !request.Valid() {
		return sharedjob.Record{}, false, ErrBackupCommandInvalid
	}

	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	defer tx.Rollback()
	if existing, found, findErr := findSharedJobByKey(ctx, tx, s.projectID, idempotencyKey); findErr != nil {
		return sharedjob.Record{}, false, findErr
	} else if found {
		if !existing.Request().Equivalent(request) {
			return sharedjob.Record{}, false, ErrJobIdempotencyConflict
		}
		stored, loadErr := scanBackupCommand(tx.QueryRowContext(ctx, `SELECT command_json FROM backup_commands WHERE job_id=? AND project_uuid=? AND command_hash=?`, existing.ID, s.projectID, hash))
		if loadErr != nil || stored != command {
			return sharedjob.Record{}, false, ErrJobIdempotencyConflict
		}
		return existing, true, tx.Commit()
	}
	id, err := domain.NewID()
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	now := s.now().UTC()
	record := sharedjob.Record{ID: id, ProjectID: request.ProjectID, Kind: request.Kind, RevisionID: request.RevisionID, InputHash: hash, IdempotencyKey: idempotencyKey, RequestHash: hash, Status: sharedjob.Queued, CreatedAt: now, UpdatedAt: now}
	if !record.Valid() {
		return sharedjob.Record{}, false, ErrBackupCommandInvalid
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, record.ID, record.ProjectID, record.Kind, nullableID(record.RevisionID), record.InputHash, record.IdempotencyKey, record.RequestHash, record.Status, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		return sharedjob.Record{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO backup_commands(job_id,project_uuid,command_hash,command_json,created_at) VALUES(?,?,?,?,?)`, record.ID, record.ProjectID, hash, encoded, now.Format(time.RFC3339Nano)); err != nil {
		return sharedjob.Record{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return sharedjob.Record{}, false, err
	}
	return record, false, nil
}

func (s *Store) GetBackupCommand(ctx context.Context, jobID domain.ID) (backupdomain.Command, error) {
	if !jobID.Valid() {
		return backupdomain.Command{}, ErrBackupCommandNotFound
	}
	command, err := scanBackupCommand(s.db.QueryRowContext(ctx, `SELECT c.command_json FROM backup_commands c JOIN jobs j ON j.id=c.job_id WHERE c.job_id=? AND c.project_uuid=? AND j.kind='backup'`, jobID, s.projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return backupdomain.Command{}, ErrBackupCommandNotFound
	}
	return command, err
}

// BeginBackupPublication is the durable non-interruptible boundary. It races
// with RequestCancellation under the same Store write lane.
func (s *Store) BeginBackupPublication(ctx context.Context, jobID domain.ID, cancelGeneration int64, startedAt time.Time) (bool, error) {
	if !jobID.Valid() || cancelGeneration != 0 || startedAt.IsZero() {
		return false, ErrBackupCommandInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	write, err := s.db.ExecContext(ctx, `UPDATE backup_commands SET publication_started_at=COALESCE(publication_started_at,?) WHERE job_id=? AND project_uuid=? AND EXISTS (SELECT 1 FROM jobs WHERE id=? AND project_uuid=? AND kind='backup' AND status IN ('running','interrupted') AND cancel_generation=?)`, startedAt.UTC().Format(time.RFC3339Nano), jobID, s.projectID, jobID, s.projectID, cancelGeneration)
	if err != nil {
		return false, err
	}
	rows, err := write.RowsAffected()
	return rows == 1, err
}

type backupCommandScanner interface{ Scan(...any) error }

func scanBackupCommand(scanner backupCommandScanner) (backupdomain.Command, error) {
	var encoded []byte
	if err := scanner.Scan(&encoded); err != nil {
		return backupdomain.Command{}, err
	}
	var command backupdomain.Command
	if err := backupdomain.DecodeCommand(encoded, &command); err != nil {
		return backupdomain.Command{}, ErrBackupCommandInvalid
	}
	return command, nil
}
