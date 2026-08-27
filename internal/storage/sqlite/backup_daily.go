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
	ErrDailyAdmissionInvalid  = errors.New("daily backup admission is invalid")
	ErrDailyAdmissionConflict = errors.New("daily backup admission conflicts with existing state")
)

var _ ports.DailyAdmissionStore = (*Store)(nil)

func (s *Store) EnsureDailyAdmission(ctx context.Context, record ports.DailyAdmissionRecord) (ports.DailyAdmissionRecord, bool, error) {
	if record.ProjectID != s.projectID || !record.ProjectID.Valid() || !record.JobID.Valid() || len(record.CommandHash) != 64 || record.CreatedAt.IsZero() {
		return ports.DailyAdmissionRecord{}, false, ErrDailyAdmissionInvalid
	}
	if parsed, err := time.Parse("2006-01-02", record.LocalDate); err != nil || parsed.Format("2006-01-02") != record.LocalDate {
		return ports.DailyAdmissionRecord{}, false, ErrDailyAdmissionInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	write, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO daily_backup_admission(project_uuid,local_date,job_id,command_hash,created_at) VALUES(?,?,?,?,?)`, record.ProjectID, record.LocalDate, record.JobID, record.CommandHash, record.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return ports.DailyAdmissionRecord{}, false, err
	}
	rows, err := write.RowsAffected()
	if err != nil {
		return ports.DailyAdmissionRecord{}, false, err
	}
	existing, found, err := s.GetDailyAdmission(ctx, record.ProjectID, record.LocalDate)
	if err != nil || !found {
		return ports.DailyAdmissionRecord{}, false, err
	}
	if existing.ProjectID != record.ProjectID || existing.LocalDate != record.LocalDate || existing.JobID != record.JobID || existing.CommandHash != record.CommandHash {
		return ports.DailyAdmissionRecord{}, false, ErrDailyAdmissionConflict
	}
	return existing, rows == 0, nil
}

func (s *Store) ReplaceFailedDailyAdmission(ctx context.Context, record ports.DailyAdmissionRecord, expectedJobID domain.ID) (ports.DailyAdmissionRecord, bool, error) {
	if record.ProjectID != s.projectID || !record.ProjectID.Valid() || !record.JobID.Valid() || !expectedJobID.Valid() || len(record.CommandHash) != 64 || record.CreatedAt.IsZero() {
		return ports.DailyAdmissionRecord{}, false, ErrDailyAdmissionInvalid
	}
	if parsed, err := time.Parse("2006-01-02", record.LocalDate); err != nil || parsed.Format("2006-01-02") != record.LocalDate {
		return ports.DailyAdmissionRecord{}, false, ErrDailyAdmissionInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	write, err := s.db.ExecContext(ctx, `UPDATE daily_backup_admission SET job_id=?,command_hash=?,created_at=? WHERE project_uuid=? AND local_date=? AND job_id=?`, record.JobID, record.CommandHash, record.CreatedAt.UTC().Format(time.RFC3339Nano), record.ProjectID, record.LocalDate, expectedJobID)
	if err != nil {
		return ports.DailyAdmissionRecord{}, false, err
	}
	rows, err := write.RowsAffected()
	if err != nil {
		return ports.DailyAdmissionRecord{}, false, err
	}
	existing, found, err := s.GetDailyAdmission(ctx, record.ProjectID, record.LocalDate)
	if err != nil || !found {
		return ports.DailyAdmissionRecord{}, false, err
	}
	if existing.ProjectID != record.ProjectID || existing.LocalDate != record.LocalDate || existing.JobID != record.JobID || existing.CommandHash != record.CommandHash {
		return ports.DailyAdmissionRecord{}, false, ErrDailyAdmissionConflict
	}
	return existing, rows == 0, nil
}

func (s *Store) GetDailyAdmission(ctx context.Context, projectID domain.ID, localDate string) (ports.DailyAdmissionRecord, bool, error) {
	if projectID != s.projectID || !projectID.Valid() {
		return ports.DailyAdmissionRecord{}, false, ErrDailyAdmissionInvalid
	}
	var record ports.DailyAdmissionRecord
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT project_uuid,local_date,job_id,command_hash,created_at FROM daily_backup_admission WHERE project_uuid=? AND local_date=?`, projectID, localDate).Scan(&record.ProjectID, &record.LocalDate, &record.JobID, &record.CommandHash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.DailyAdmissionRecord{}, false, nil
	}
	if err != nil {
		return ports.DailyAdmissionRecord{}, false, err
	}
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return ports.DailyAdmissionRecord{}, false, ErrDailyAdmissionInvalid
	}
	return record, true, nil
}

func (s *Store) PutDailyWaiver(ctx context.Context, waiver backupdomain.DailyWaiver) (bool, error) {
	if !waiver.Valid() || waiver.ProjectID != s.projectID {
		return false, ErrDailyAdmissionInvalid
	}
	encoded, err := waiver.CanonicalJSON()
	if err != nil {
		return false, err
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	write, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO daily_backup_waivers(project_uuid,local_date,failed_job_id,waiver_json,confirmed_at) VALUES(?,?,?,?,?)`, waiver.ProjectID, waiver.LocalDate, waiver.FailedJobID, encoded, waiver.ConfirmedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	rows, err := write.RowsAffected()
	if err != nil {
		return false, err
	}
	existing, found, err := s.GetDailyWaiver(ctx, waiver.ProjectID, waiver.LocalDate)
	if err != nil || !found {
		return false, err
	}
	if existing != waiver {
		return false, ErrDailyAdmissionConflict
	}
	return rows == 0, nil
}

func (s *Store) GetDailyWaiver(ctx context.Context, projectID domain.ID, localDate string) (backupdomain.DailyWaiver, bool, error) {
	if projectID != s.projectID || !projectID.Valid() {
		return backupdomain.DailyWaiver{}, false, ErrDailyAdmissionInvalid
	}
	var encoded []byte
	err := s.db.QueryRowContext(ctx, `SELECT waiver_json FROM daily_backup_waivers WHERE project_uuid=? AND local_date=?`, projectID, localDate).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return backupdomain.DailyWaiver{}, false, nil
	}
	if err != nil {
		return backupdomain.DailyWaiver{}, false, err
	}
	waiver, err := backupdomain.DecodeDailyWaiver(encoded)
	if err != nil {
		return backupdomain.DailyWaiver{}, false, ErrDailyAdmissionInvalid
	}
	return waiver, true, nil
}
