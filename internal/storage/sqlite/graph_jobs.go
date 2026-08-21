package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var (
	ErrGraphJobConflict   = errors.New("graph job idempotency conflict")
	ErrGraphJobNotFound   = errors.New("graph job not found")
	ErrGraphJobRetry      = errors.New("graph retry target is invalid")
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
	if request.RetryOfJobID != "" {
		previous, previousFound, previousErr := findGraphJob(ctx, tx, request.ProjectID, request.RetryOfJobID)
		if previousErr != nil {
			return graphsync.GraphJob{}, false, previousErr
		}
		if !previousFound || previous.RevisionID != request.RevisionID || previous.InputHash != request.InputHash {
			return graphsync.GraphJob{}, false, ErrGraphJobRetry
		}
	}
	idValue, err := domain.NewID()
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	now := s.now().UTC()
	job := graphsync.GraphJob{ID: idValue, RetryOfJobID: request.RetryOfJobID, ProjectID: request.ProjectID, RevisionID: request.RevisionID, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, Evidence: request.Evidence, Status: graphsync.JobQueued, CreatedAt: now, UpdatedAt: now}
	if !job.Valid() {
		return graphsync.GraphJob{}, false, ErrGraphSyncStateInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,retry_of_job_id,graph_evidence,status,created_at,updated_at) VALUES(?,?, 'graph_sync',?,?,?,?,?,?,'queued',?,?)`, idValue, request.ProjectID, request.RevisionID, request.InputHash, request.IdempotencyKey, request.RequestHash, nullID(request.RetryOfJobID), nullString(request.Evidence), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	return job, false, tx.Commit()
}

var _ graphsync.JobAdmission = (*Store)(nil)
var _ graphsync.DurableJobStore = (*Store)(nil)
var _ graphsync.JobEventStore = (*Store)(nil)
var _ graphsync.CancellationIntentStore = (*Store)(nil)

func (s *Store) GetGraphJob(ctx context.Context, jobID domain.ID) (graphsync.GraphJob, error) {
	if !jobID.Valid() {
		return graphsync.GraphJob{}, ErrGraphJobNotFound
	}
	job, err := scanGraphJob(s.db.QueryRowContext(ctx, `SELECT id,project_uuid,revision_id,input_hash,idempotency_key,request_hash,retry_of_job_id,COALESCE(graph_evidence,''),status,result_type,result_id,result_url,cancel_generation,cancel_requested_at,created_at,updated_at FROM jobs WHERE id=? AND project_uuid=? AND kind='graph_sync'`, jobID, s.projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return graphsync.GraphJob{}, ErrGraphJobNotFound
	}
	return job, err
}

func (s *Store) RequestGraphCancellation(ctx context.Context, jobID domain.ID) (graphsync.GraphJob, bool, error) {
	record, replay, err := s.RequestCancellation(ctx, jobID)
	if errors.Is(err, ErrJobNotFound) {
		return graphsync.GraphJob{}, false, ErrGraphJobNotFound
	}
	if err != nil || record.Kind != graphsync.SharedJobKind {
		return graphsync.GraphJob{}, false, err
	}
	job, getErr := s.GetGraphJob(ctx, record.ID)
	return job, replay, getErr
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
	shared, err := s.GetJob(ctx, jobID)
	if errors.Is(err, ErrJobNotFound) || shared.Kind != graphsync.SharedJobKind {
		return graphsync.GraphJob{}, false, ErrGraphJobNotFound
	}
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	var sharedResult *sharedjob.Result
	if result != nil {
		sharedResult = &sharedjob.Result{Type: result.Type, ID: result.ID, URL: result.URL}
	}
	_, updated, err := s.Transition(ctx, jobID, sharedjob.Status(expected), sharedjob.Status(next), sharedResult, shared.CancelGeneration)
	if err != nil || !updated {
		return graphsync.GraphJob{}, updated, err
	}
	job, err := s.GetGraphJob(ctx, jobID)
	return job, true, err
}

func (s *Store) AppendGraphJobEvent(ctx context.Context, event graphsync.GraphJobEvent) (graphsync.GraphJobEvent, bool, error) {
	if !event.Valid() {
		return graphsync.GraphJobEvent{}, false, ErrGraphJobEvent
	}
	history, err := s.ListGraphJobEvents(ctx, event.JobID, 0)
	if err != nil {
		return graphsync.GraphJobEvent{}, false, err
	}
	if len(history) > 0 {
		previous := history[len(history)-1]
		if event.Ordinal > previous.Ordinal && (event.Progress < previous.Progress || !previous.Phase.CanAdvanceTo(event.Phase)) {
			return graphsync.GraphJobEvent{}, false, ErrGraphJobEvent
		}
	}
	previousEvents, err := s.ListEvents(ctx, event.JobID, event.Ordinal-1)
	if err != nil {
		return graphsync.GraphJobEvent{}, false, err
	}
	createdAt := s.now().UTC()
	if len(previousEvents) > 0 && previousEvents[0].Ordinal == event.Ordinal {
		createdAt = previousEvents[0].CreatedAt
	}
	if len(previousEvents) > 0 && previousEvents[0].Ordinal != event.Ordinal {
		previous := previousEvents[len(previousEvents)-1]
		if event.Progress < previous.Progress {
			return graphsync.GraphJobEvent{}, false, ErrGraphJobEvent
		}
	}
	stored, replay, err := s.Append(ctx, sharedjob.Event{JobID: event.JobID, Ordinal: event.Ordinal, Phase: string(event.Phase), Progress: event.Progress, Warning: event.Warning, SafeError: event.SafeError, Result: graphResultToShared(event.Result), CreatedAt: createdAt})
	if errors.Is(err, ErrJobEvent) {
		return graphsync.GraphJobEvent{}, false, ErrGraphJobEvent
	}
	if errors.Is(err, ErrJobNotFound) {
		return graphsync.GraphJobEvent{}, false, ErrGraphJobNotFound
	}
	if err != nil {
		return graphsync.GraphJobEvent{}, false, err
	}
	return graphEventFromShared(stored), replay, nil
}

func (s *Store) ListGraphJobEvents(ctx context.Context, jobID domain.ID, after int64) ([]graphsync.GraphJobEvent, error) {
	if !jobID.Valid() || after < 0 {
		return nil, ErrGraphJobNotFound
	}
	stored, err := s.ListEvents(ctx, jobID, after)
	if err != nil {
		return nil, err
	}
	events := make([]graphsync.GraphJobEvent, 0, len(stored))
	for _, event := range stored {
		events = append(events, graphEventFromShared(event))
	}
	return events, nil
}

func findGraphJobByKey(ctx context.Context, tx *sql.Tx, projectID domain.ID, key string) (graphsync.GraphJob, bool, error) {
	job, err := scanGraphJob(tx.QueryRowContext(ctx, `SELECT id,project_uuid,revision_id,input_hash,idempotency_key,request_hash,retry_of_job_id,COALESCE(graph_evidence,''),status,result_type,result_id,result_url,cancel_generation,cancel_requested_at,created_at,updated_at FROM jobs WHERE project_uuid=? AND idempotency_key=? AND kind='graph_sync'`, projectID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return graphsync.GraphJob{}, false, nil
	}
	return job, err == nil, err
}

func findGraphJob(ctx context.Context, tx *sql.Tx, projectID, jobID domain.ID) (graphsync.GraphJob, bool, error) {
	job, err := scanGraphJob(tx.QueryRowContext(ctx, `SELECT id,project_uuid,revision_id,input_hash,idempotency_key,request_hash,retry_of_job_id,COALESCE(graph_evidence,''),status,result_type,result_id,result_url,cancel_generation,cancel_requested_at,created_at,updated_at FROM jobs WHERE id=? AND project_uuid=? AND kind='graph_sync'`, jobID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return graphsync.GraphJob{}, false, nil
	}
	return job, err == nil, err
}

type graphJobScanner interface{ Scan(...any) error }

func scanGraphJob(scanner graphJobScanner) (graphsync.GraphJob, error) {
	var id, projectID, revisionID, inputHash, key, requestHash, evidence, status string
	var retryOf sql.NullString
	var resultType, resultID, resultURL, canceledRaw sql.NullString
	var cancelGeneration int64
	var createdAt, updatedAt string
	if err := scanner.Scan(&id, &projectID, &revisionID, &inputHash, &key, &requestHash, &retryOf, &evidence, &status, &resultType, &resultID, &resultURL, &cancelGeneration, &canceledRaw, &createdAt, &updatedAt); err != nil {
		return graphsync.GraphJob{}, err
	}
	created, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return graphsync.GraphJob{}, err
	}
	updated, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return graphsync.GraphJob{}, err
	}
	var canceledAt *time.Time
	if canceledRaw.Valid {
		value, parseErr := time.Parse(time.RFC3339Nano, canceledRaw.String)
		if parseErr != nil {
			return graphsync.GraphJob{}, parseErr
		}
		canceledAt = &value
	}
	job := graphsync.GraphJob{ID: domain.ID(id), RetryOfJobID: domain.ID(retryOf.String), ProjectID: domain.ID(projectID), RevisionID: domain.ID(revisionID), InputHash: inputHash, IdempotencyKey: key, RequestHash: requestHash, Evidence: evidence, Status: graphsync.JobStatus(status), CancelGeneration: cancelGeneration, CancelRequestedAt: canceledAt, CreatedAt: created, UpdatedAt: updated}
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

func graphResultToShared(result *graphsync.GraphJobResult) *sharedjob.Result {
	if result == nil {
		return nil
	}
	return &sharedjob.Result{Type: result.Type, ID: result.ID, URL: result.URL}
}

func graphEventFromShared(event sharedjob.Event) graphsync.GraphJobEvent {
	result := (*graphsync.GraphJobResult)(nil)
	if event.Result != nil {
		result = &graphsync.GraphJobResult{Type: event.Result.Type, ID: event.Result.ID, URL: event.Result.URL}
	}
	return graphsync.GraphJobEvent{JobID: event.JobID, Ordinal: event.Ordinal, Phase: graphsync.WorkerPhase(event.Phase), Progress: event.Progress, Warning: event.Warning, SafeError: event.SafeError, Result: result}
}
