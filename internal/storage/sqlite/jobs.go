package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var (
	ErrJobInvalid             = errors.New("shared job is invalid")
	ErrJobNotFound            = errors.New("shared job not found")
	ErrJobProjectScope        = errors.New("shared job project is not active")
	ErrJobIdempotencyConflict = errors.New("shared job idempotency conflict")
	ErrJobTransition          = errors.New("shared job transition is invalid")
	ErrJobEvent               = errors.New("shared job event is invalid")
)

var _ sharedjob.Store = (*Store)(nil)
var _ sharedjob.RecoverableStore = (*Store)(nil)
var _ sharedjob.EventStore = (*Store)(nil)

func (s *Store) CreateOrGet(ctx context.Context, request sharedjob.Request) (sharedjob.Record, bool, error) {
	if !request.Valid() {
		return sharedjob.Record{}, false, ErrJobInvalid
	}
	if request.ProjectID != s.projectID {
		return sharedjob.Record{}, false, ErrJobProjectScope
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	defer tx.Rollback()
	if existing, found, findErr := findSharedJobByKey(ctx, tx, s.projectID, request.IdempotencyKey); findErr != nil {
		return sharedjob.Record{}, false, findErr
	} else if found {
		if !existing.Request().Equivalent(request) {
			return sharedjob.Record{}, false, ErrJobIdempotencyConflict
		}
		return existing, true, tx.Commit()
	}
	id, err := domain.NewID()
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	now := s.now().UTC()
	record := sharedjob.Record{ID: id, ProjectID: request.ProjectID, Kind: request.Kind, RevisionID: request.RevisionID, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, Status: sharedjob.Queued, CreatedAt: now, UpdatedAt: now}
	if !record.Valid() {
		return sharedjob.Record{}, false, ErrJobInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, record.ID, record.ProjectID, record.Kind, nullableID(record.RevisionID), record.InputHash, record.IdempotencyKey, record.RequestHash, record.Status, record.CreatedAt.Format(time.RFC3339Nano), record.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return sharedjob.Record{}, false, err
	}
	return record, false, nil
}

func (s *Store) GetJob(ctx context.Context, id domain.ID) (sharedjob.Record, error) {
	if !id.Valid() {
		return sharedjob.Record{}, ErrJobNotFound
	}
	record, err := scanSharedJob(s.db.QueryRowContext(ctx, sharedJobSelect+` WHERE id=? AND project_uuid=?`, id, s.projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return sharedjob.Record{}, ErrJobNotFound
	}
	return record, err
}

func (s *Store) ListRecoverableJobs(ctx context.Context, limit int) ([]sharedjob.Record, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrJobInvalid
	}
	rows, err := s.db.QueryContext(ctx, sharedJobSelect+` WHERE project_uuid=? AND status IN ('queued','running','interrupted') ORDER BY created_at,id LIMIT ?`, s.projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]sharedjob.Record, 0)
	for rows.Next() {
		record, scanErr := scanSharedJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, record)
	}
	return result, rows.Err()
}

func (s *Store) Transition(ctx context.Context, id domain.ID, expected, next sharedjob.Status, result *sharedjob.Result, observedCancelGeneration int64) (sharedjob.Record, bool, error) {
	if !id.Valid() || !expected.Valid() || !next.Valid() || observedCancelGeneration < 0 || (next == sharedjob.Succeeded && result == nil) || (result != nil && !result.Valid()) {
		return sharedjob.Record{}, false, ErrJobTransition
	}
	if expected == next {
		record, err := s.GetJob(ctx, id)
		if err != nil || record.Status != next || !sameSharedResult(record.Result, result) || record.CancelGeneration != observedCancelGeneration {
			return sharedjob.Record{}, false, err
		}
		return record, true, nil
	}
	if !expected.CanTransitionTo(next) {
		return sharedjob.Record{}, false, ErrJobTransition
	}
	if next == sharedjob.Succeeded && observedCancelGeneration != 0 {
		return sharedjob.Record{}, false, ErrJobTransition
	}
	var resultType, resultID, resultURL any
	if result != nil {
		resultType, resultID, resultURL = result.Type, result.ID, result.URL
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	write, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?,result_type=?,result_id=?,result_url=?,updated_at=? WHERE id=? AND project_uuid=? AND status=? AND cancel_generation=?`, next, resultType, resultID, resultURL, s.now().UTC().Format(time.RFC3339Nano), id, s.projectID, expected, observedCancelGeneration)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	changed, err := write.RowsAffected()
	if err != nil || changed == 0 {
		return sharedjob.Record{}, false, err
	}
	record, err := s.GetJob(ctx, id)
	return record, true, err
}

func (s *Store) RequestCancellation(ctx context.Context, id domain.ID) (sharedjob.Record, bool, error) {
	if !id.Valid() {
		return sharedjob.Record{}, false, ErrJobNotFound
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	defer tx.Rollback()
	record, err := scanSharedJob(tx.QueryRowContext(ctx, sharedJobSelect+` WHERE id=? AND project_uuid=?`, id, s.projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return sharedjob.Record{}, false, ErrJobNotFound
	}
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	if record.Status.Terminal() || record.CancelGeneration > 0 {
		return record, true, tx.Commit()
	}
	if record.Kind == "backup" {
		var publicationStarted int
		if queryErr := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM backup_commands WHERE job_id=? AND project_uuid=? AND publication_started_at IS NOT NULL)`, id, s.projectID).Scan(&publicationStarted); queryErr != nil {
			return sharedjob.Record{}, false, queryErr
		}
		if publicationStarted != 0 {
			// Publication is deliberately non-interruptible. Returning the
			// unchanged record lets the unified endpoint report authoritative
			// running state without recording a cancellation generation.
			return record, true, tx.Commit()
		}
	}
	if err = s.inject("shared-job-cancel-before-write"); err != nil {
		return sharedjob.Record{}, false, err
	}
	now := s.now().UTC()
	_, err = tx.ExecContext(ctx, `UPDATE jobs SET cancel_generation=cancel_generation+1,cancel_requested_at=?,updated_at=? WHERE id=? AND project_uuid=? AND cancel_generation=?`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), id, s.projectID, record.CancelGeneration)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	if err = s.inject("shared-job-cancel-after-write"); err != nil {
		return sharedjob.Record{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return sharedjob.Record{}, false, err
	}
	updated, err := s.GetJob(ctx, id)
	return updated, false, err
}

func (s *Store) Append(ctx context.Context, event sharedjob.Event) (sharedjob.Event, bool, error) {
	if !event.Valid() {
		return sharedjob.Event{}, false, ErrJobEvent
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sharedjob.Event{}, false, err
	}
	defer tx.Rollback()
	existing, found, err := findSharedEvent(ctx, tx, s.projectID, event.JobID, event.Ordinal)
	if err != nil {
		return sharedjob.Event{}, false, err
	}
	if found {
		if !sameSharedEvent(existing, event) {
			return sharedjob.Event{}, false, ErrJobEvent
		}
		return existing, true, tx.Commit()
	}
	if previous, found, findErr := latestSharedEvent(ctx, tx, s.projectID, event.JobID); findErr != nil {
		return sharedjob.Event{}, false, findErr
	} else if found && (event.Ordinal <= previous.Ordinal || event.Progress < previous.Progress) {
		return sharedjob.Event{}, false, ErrJobEvent
	}
	var resultType, resultID, resultURL any
	if event.Result != nil {
		resultType, resultID, resultURL = event.Result.Type, event.Result.ID, event.Result.URL
	}
	write, err := tx.ExecContext(ctx, `INSERT INTO job_events(job_id,event_ordinal,phase,progress,warning,error,result_type,result_id,result_url,created_at) SELECT ?,?,?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM jobs WHERE id=? AND project_uuid=?)`, event.JobID, event.Ordinal, event.Phase, event.Progress, nullString(event.Warning), nullString(event.SafeError), resultType, resultID, resultURL, event.CreatedAt.UTC().Format(time.RFC3339Nano), event.JobID, s.projectID)
	if err != nil {
		return sharedjob.Event{}, false, err
	}
	if rows, rowsErr := write.RowsAffected(); rowsErr != nil {
		return sharedjob.Event{}, false, rowsErr
	} else if rows != 1 {
		return sharedjob.Event{}, false, ErrJobNotFound
	}
	if err = tx.Commit(); err != nil {
		return sharedjob.Event{}, false, err
	}
	return event, false, nil
}

func (s *Store) ListEvents(ctx context.Context, id domain.ID, after int64) ([]sharedjob.Event, error) {
	if !id.Valid() || after < 0 {
		return nil, ErrJobNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.job_id,e.event_ordinal,e.phase,e.progress,e.warning,e.error,e.result_type,e.result_id,e.result_url,e.created_at FROM job_events e JOIN jobs j ON j.id=e.job_id WHERE e.job_id=? AND j.project_uuid=? AND e.event_ordinal>? ORDER BY e.event_ordinal`, id, s.projectID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []sharedjob.Event{}
	for rows.Next() {
		event, scanErr := scanSharedEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

const sharedJobSelect = `SELECT id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,status,result_type,result_id,result_url,cancel_generation,cancel_requested_at,created_at,updated_at FROM jobs`

type sharedJobScanner interface{ Scan(...any) error }

func scanSharedJob(scanner sharedJobScanner) (sharedjob.Record, error) {
	var id, projectID, kind, inputHash, key, requestHash, status, createdRaw, updatedRaw string
	var revisionID, resultType, resultID, resultURL, canceledRaw sql.NullString
	var generation int64
	if err := scanner.Scan(&id, &projectID, &kind, &revisionID, &inputHash, &key, &requestHash, &status, &resultType, &resultID, &resultURL, &generation, &canceledRaw, &createdRaw, &updatedRaw); err != nil {
		return sharedjob.Record{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return sharedjob.Record{}, err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedRaw)
	if err != nil {
		return sharedjob.Record{}, err
	}
	var canceledAt *time.Time
	if canceledRaw.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, canceledRaw.String)
		if parseErr != nil {
			return sharedjob.Record{}, parseErr
		}
		canceledAt = &value
	}
	var result *sharedjob.Result
	if resultType.Valid || resultID.Valid || resultURL.Valid {
		if !resultType.Valid || !resultID.Valid || !resultURL.Valid {
			return sharedjob.Record{}, fmt.Errorf("partial stored shared job result")
		}
		result = &sharedjob.Result{Type: resultType.String, ID: domain.ID(resultID.String), URL: resultURL.String}
	}
	record := sharedjob.Record{ID: domain.ID(id), ProjectID: domain.ID(projectID), Kind: sharedjob.Kind(kind), RevisionID: domain.ID(revisionID.String), InputHash: inputHash, IdempotencyKey: key, RequestHash: requestHash, Status: sharedjob.Status(status), Result: result, CancelGeneration: generation, CancelRequestedAt: canceledAt, CreatedAt: createdAt, UpdatedAt: updatedAt}
	if !record.Valid() {
		return sharedjob.Record{}, fmt.Errorf("invalid stored shared job")
	}
	return record, nil
}

type sharedEventScanner interface{ Scan(...any) error }

func scanSharedEvent(scanner sharedEventScanner) (sharedjob.Event, error) {
	var jobID, phase, createdRaw string
	var ordinal int64
	var progress int
	var warning, safeError, resultType, resultID, resultURL sql.NullString
	if err := scanner.Scan(&jobID, &ordinal, &phase, &progress, &warning, &safeError, &resultType, &resultID, &resultURL, &createdRaw); err != nil {
		return sharedjob.Event{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return sharedjob.Event{}, err
	}
	var result *sharedjob.Result
	if resultType.Valid || resultID.Valid || resultURL.Valid {
		if !resultType.Valid || !resultID.Valid || !resultURL.Valid {
			return sharedjob.Event{}, fmt.Errorf("partial stored shared job event result")
		}
		result = &sharedjob.Result{Type: resultType.String, ID: domain.ID(resultID.String), URL: resultURL.String}
	}
	event := sharedjob.Event{JobID: domain.ID(jobID), Ordinal: ordinal, Phase: phase, Progress: progress, Warning: warning.String, SafeError: safeError.String, Result: result, CreatedAt: createdAt}
	if !event.Valid() {
		return sharedjob.Event{}, fmt.Errorf("invalid stored shared job event")
	}
	return event, nil
}

func findSharedJobByKey(ctx context.Context, tx *sql.Tx, projectID domain.ID, key string) (sharedjob.Record, bool, error) {
	record, err := scanSharedJob(tx.QueryRowContext(ctx, sharedJobSelect+` WHERE project_uuid=? AND idempotency_key=?`, projectID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return sharedjob.Record{}, false, nil
	}
	return record, err == nil, err
}

func findSharedEvent(ctx context.Context, tx *sql.Tx, projectID, jobID domain.ID, ordinal int64) (sharedjob.Event, bool, error) {
	event, err := scanSharedEvent(tx.QueryRowContext(ctx, `SELECT e.job_id,e.event_ordinal,e.phase,e.progress,e.warning,e.error,e.result_type,e.result_id,e.result_url,e.created_at FROM job_events e JOIN jobs j ON j.id=e.job_id WHERE e.job_id=? AND j.project_uuid=? AND e.event_ordinal=?`, jobID, projectID, ordinal))
	if errors.Is(err, sql.ErrNoRows) {
		return sharedjob.Event{}, false, nil
	}
	return event, err == nil, err
}

func latestSharedEvent(ctx context.Context, tx *sql.Tx, projectID, jobID domain.ID) (sharedjob.Event, bool, error) {
	event, err := scanSharedEvent(tx.QueryRowContext(ctx, `SELECT e.job_id,e.event_ordinal,e.phase,e.progress,e.warning,e.error,e.result_type,e.result_id,e.result_url,e.created_at FROM job_events e JOIN jobs j ON j.id=e.job_id WHERE e.job_id=? AND j.project_uuid=? ORDER BY e.event_ordinal DESC LIMIT 1`, jobID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return sharedjob.Event{}, false, nil
	}
	return event, err == nil, err
}

func sameSharedResult(left, right *sharedjob.Result) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func sameSharedEvent(left, right sharedjob.Event) bool {
	return left.JobID == right.JobID && left.Ordinal == right.Ordinal && left.Phase == right.Phase && left.Progress == right.Progress && left.Warning == right.Warning && left.SafeError == right.SafeError && sameSharedResult(left.Result, right.Result) && left.CreatedAt.Equal(right.CreatedAt)
}

func nullableID(id domain.ID) any {
	if id == "" {
		return nil
	}
	return id
}
