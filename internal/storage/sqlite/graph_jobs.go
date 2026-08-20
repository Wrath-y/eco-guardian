package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

var (
	ErrGraphJobConflict   = errors.New("graph job idempotency conflict")
	ErrGraphJobNotFound   = errors.New("graph job not found")
	ErrGraphJobTransition = errors.New("graph job transition is invalid")
	ErrGraphJobEvent      = errors.New("graph job event conflicts with prior checkpoint")
)

func (s *Store) CreateOrGetGraphJob(ctx context.Context, request graphsync.GraphJobRequest) (graphsync.GraphJob, bool, error) {
	if !request.Valid() || request.ProjectID != s.projectID {
		return graphsync.GraphJob{}, false, ErrGraphSyncStateInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	defer tx.Rollback()
	existing, found, err := findGraphJobByKey(ctx, tx, request.ProjectID, request.IdempotencyKey)
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	if found {
		if existing.RequestHash != request.RequestHash || existing.Evidence != request.Evidence {
			return graphsync.GraphJob{}, false, ErrGraphJobConflict
		}
		return existing, true, tx.Commit()
	}
	idValue, err := domain.NewID()
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	now := s.now().UTC()
	job := graphsync.GraphJob{ID: idValue, ProjectID: request.ProjectID, RevisionID: request.RevisionID, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, Evidence: request.Evidence, Status: graphsync.JobQueued}
	if !job.Valid() {
		return graphsync.GraphJob{}, false, ErrGraphSyncStateInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,graph_evidence,status,created_at,updated_at) VALUES(?,?, 'graph_sync',?,?,?,?,?,'queued',?,?)`, idValue, request.ProjectID, request.RevisionID, request.InputHash, request.IdempotencyKey, request.RequestHash, nullString(request.Evidence), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	return job, false, tx.Commit()
}

var _ graphsync.JobAdmission = (*Store)(nil)
var _ graphsync.DurableJobStore = (*Store)(nil)
var _ graphsync.JobEventStore = (*Store)(nil)

func (s *Store) GetGraphJob(ctx context.Context, jobID domain.ID) (graphsync.GraphJob, error) {
	if !jobID.Valid() {
		return graphsync.GraphJob{}, ErrGraphJobNotFound
	}
	job, err := scanGraphJob(s.db.QueryRowContext(ctx, `SELECT id,project_uuid,revision_id,input_hash,idempotency_key,request_hash,COALESCE(graph_evidence,''),status,result_type,result_id,result_url FROM jobs WHERE id=? AND project_uuid=? AND kind='graph_sync'`, jobID, s.projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return graphsync.GraphJob{}, ErrGraphJobNotFound
	}
	return job, err
}

func (s *Store) TransitionGraphJob(ctx context.Context, jobID domain.ID, expected, next graphsync.JobStatus, result *graphsync.GraphJobResult) (graphsync.GraphJob, bool, error) {
	if !jobID.Valid() || !expected.Valid() || !next.Valid() || (result != nil && !result.Valid()) || (next == graphsync.JobSucceeded && result == nil) {
		return graphsync.GraphJob{}, false, ErrGraphJobTransition
	}
	if expected == next && next.Terminal() {
		job, err := s.GetGraphJob(ctx, jobID)
		if err != nil || job.Status != next || !sameGraphJobResult(job.Result, result) {
			return graphsync.GraphJob{}, false, err
		}
		return job, true, nil
	}
	if !expected.CanTransitionTo(next) {
		return graphsync.GraphJob{}, false, ErrGraphJobTransition
	}
	var resultType, resultID, resultURL any
	if result != nil {
		resultType, resultID, resultURL = result.Type, result.ID, result.URL
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	write, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?,result_type=?,result_id=?,result_url=?,updated_at=? WHERE id=? AND project_uuid=? AND kind='graph_sync' AND status=?`, next, resultType, resultID, resultURL, s.now().UTC().Format(time.RFC3339Nano), jobID, s.projectID, expected)
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	updated, err := write.RowsAffected()
	if err != nil || updated == 0 {
		return graphsync.GraphJob{}, false, err
	}
	job, err := s.GetGraphJob(ctx, jobID)
	return job, true, err
}

func (s *Store) AppendGraphJobEvent(ctx context.Context, event graphsync.GraphJobEvent) (graphsync.GraphJobEvent, bool, error) {
	if !event.Valid() {
		return graphsync.GraphJobEvent{}, false, ErrGraphJobEvent
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return graphsync.GraphJobEvent{}, false, err
	}
	defer tx.Rollback()
	existing, found, err := findGraphJobEvent(ctx, tx, s.projectID, event.JobID, event.Ordinal)
	if err != nil {
		return graphsync.GraphJobEvent{}, false, err
	}
	if found {
		if !sameGraphJobEvent(existing, event) {
			return graphsync.GraphJobEvent{}, false, ErrGraphJobEvent
		}
		return existing, true, tx.Commit()
	}
	previous, found, err := latestGraphJobEvent(ctx, tx, s.projectID, event.JobID)
	if err != nil {
		return graphsync.GraphJobEvent{}, false, err
	}
	if found && (event.Ordinal <= previous.Ordinal || event.Progress < previous.Progress || !previous.Phase.CanAdvanceTo(event.Phase)) {
		return graphsync.GraphJobEvent{}, false, ErrGraphJobEvent
	}
	var resultType, resultID, resultURL any
	if event.Result != nil {
		resultType, resultID, resultURL = event.Result.Type, event.Result.ID, event.Result.URL
	}
	write, err := tx.ExecContext(ctx, `INSERT INTO job_events(job_id,event_ordinal,phase,progress,warning,error,result_type,result_id,result_url,created_at) SELECT ?,?,?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM jobs WHERE id=? AND project_uuid=? AND kind='graph_sync')`, event.JobID, event.Ordinal, event.Phase, event.Progress, nullString(event.Warning), nullString(event.SafeError), resultType, resultID, resultURL, s.now().UTC().Format(time.RFC3339Nano), event.JobID, s.projectID)
	if err != nil {
		return graphsync.GraphJobEvent{}, false, err
	}
	if rows, rowsErr := write.RowsAffected(); rowsErr != nil {
		return graphsync.GraphJobEvent{}, false, rowsErr
	} else if rows != 1 {
		return graphsync.GraphJobEvent{}, false, ErrGraphJobNotFound
	}
	if err = tx.Commit(); err != nil {
		return graphsync.GraphJobEvent{}, false, err
	}
	return event, false, nil
}

func (s *Store) ListGraphJobEvents(ctx context.Context, jobID domain.ID, after int64) ([]graphsync.GraphJobEvent, error) {
	if !jobID.Valid() || after < 0 {
		return nil, ErrGraphJobNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.job_id,e.event_ordinal,e.phase,e.progress,e.warning,e.error,e.result_type,e.result_id,e.result_url FROM job_events e JOIN jobs j ON j.id=e.job_id WHERE e.job_id=? AND j.project_uuid=? AND j.kind='graph_sync' AND e.event_ordinal>? ORDER BY e.event_ordinal`, jobID, s.projectID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []graphsync.GraphJobEvent{}
	for rows.Next() {
		event, scanErr := scanGraphJobEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func findGraphJobByKey(ctx context.Context, tx *sql.Tx, projectID domain.ID, key string) (graphsync.GraphJob, bool, error) {
	job, err := scanGraphJob(tx.QueryRowContext(ctx, `SELECT id,project_uuid,revision_id,input_hash,idempotency_key,request_hash,COALESCE(graph_evidence,''),status,result_type,result_id,result_url FROM jobs WHERE project_uuid=? AND idempotency_key=? AND kind='graph_sync'`, projectID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return graphsync.GraphJob{}, false, nil
	}
	return job, err == nil, err
}

type graphJobScanner interface{ Scan(...any) error }

func scanGraphJob(scanner graphJobScanner) (graphsync.GraphJob, error) {
	var id, projectID, revisionID, inputHash, key, requestHash, evidence, status string
	var resultType, resultID, resultURL sql.NullString
	if err := scanner.Scan(&id, &projectID, &revisionID, &inputHash, &key, &requestHash, &evidence, &status, &resultType, &resultID, &resultURL); err != nil {
		return graphsync.GraphJob{}, err
	}
	job := graphsync.GraphJob{ID: domain.ID(id), ProjectID: domain.ID(projectID), RevisionID: domain.ID(revisionID), InputHash: inputHash, IdempotencyKey: key, RequestHash: requestHash, Evidence: evidence, Status: graphsync.JobStatus(status)}
	if resultType.Valid || resultID.Valid || resultURL.Valid {
		if !resultType.Valid || !resultID.Valid || !resultURL.Valid {
			return graphsync.GraphJob{}, fmt.Errorf("partial stored graph job result")
		}
		job.Result = &graphsync.GraphJobResult{Type: resultType.String, ID: domain.ID(resultID.String), URL: resultURL.String}
	}
	if !job.Valid() {
		return graphsync.GraphJob{}, fmt.Errorf("invalid stored graph job")
	}
	return job, nil
}

func findGraphJobEvent(ctx context.Context, tx *sql.Tx, projectID, jobID domain.ID, ordinal int64) (graphsync.GraphJobEvent, bool, error) {
	event, err := scanGraphJobEvent(tx.QueryRowContext(ctx, `SELECT e.job_id,e.event_ordinal,e.phase,e.progress,e.warning,e.error,e.result_type,e.result_id,e.result_url FROM job_events e JOIN jobs j ON j.id=e.job_id WHERE e.job_id=? AND j.project_uuid=? AND j.kind='graph_sync' AND e.event_ordinal=?`, jobID, projectID, ordinal))
	if errors.Is(err, sql.ErrNoRows) {
		return graphsync.GraphJobEvent{}, false, nil
	}
	return event, err == nil, err
}

func latestGraphJobEvent(ctx context.Context, tx *sql.Tx, projectID, jobID domain.ID) (graphsync.GraphJobEvent, bool, error) {
	event, err := scanGraphJobEvent(tx.QueryRowContext(ctx, `SELECT e.job_id,e.event_ordinal,e.phase,e.progress,e.warning,e.error,e.result_type,e.result_id,e.result_url FROM job_events e JOIN jobs j ON j.id=e.job_id WHERE e.job_id=? AND j.project_uuid=? AND j.kind='graph_sync' ORDER BY e.event_ordinal DESC LIMIT 1`, jobID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return graphsync.GraphJobEvent{}, false, nil
	}
	return event, err == nil, err
}

type graphJobEventScanner interface{ Scan(...any) error }

func scanGraphJobEvent(scanner graphJobEventScanner) (graphsync.GraphJobEvent, error) {
	var jobID, phase string
	var ordinal int64
	var progress int
	var warning, safeError, resultType, resultID, resultURL sql.NullString
	if err := scanner.Scan(&jobID, &ordinal, &phase, &progress, &warning, &safeError, &resultType, &resultID, &resultURL); err != nil {
		return graphsync.GraphJobEvent{}, err
	}
	event := graphsync.GraphJobEvent{JobID: domain.ID(jobID), Ordinal: ordinal, Phase: graphsync.WorkerPhase(phase), Progress: progress, Warning: warning.String, SafeError: safeError.String}
	if resultType.Valid || resultID.Valid || resultURL.Valid {
		if !resultType.Valid || !resultID.Valid || !resultURL.Valid {
			return graphsync.GraphJobEvent{}, fmt.Errorf("partial stored graph job event result")
		}
		event.Result = &graphsync.GraphJobResult{Type: resultType.String, ID: domain.ID(resultID.String), URL: resultURL.String}
	}
	if !event.Valid() {
		return graphsync.GraphJobEvent{}, fmt.Errorf("invalid stored graph job event")
	}
	return event, nil
}

func sameGraphJobEvent(left, right graphsync.GraphJobEvent) bool {
	if left.JobID != right.JobID || left.Ordinal != right.Ordinal || left.Phase != right.Phase || left.Progress != right.Progress || left.Warning != right.Warning || left.SafeError != right.SafeError {
		return false
	}
	if left.Result == nil || right.Result == nil {
		return left.Result == nil && right.Result == nil
	}
	return *left.Result == *right.Result
}

func sameGraphJobResult(left, right *graphsync.GraphJobResult) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
