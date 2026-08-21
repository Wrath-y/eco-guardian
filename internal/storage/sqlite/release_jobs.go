package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

var (
	ErrReleaseJobInvalid      = errors.New("release job request is invalid")
	ErrIdempotencyConflict    = errors.New("idempotency key conflicts with a different request")
	ErrReleaseJobProjectScope = errors.New("release job project is not active")
	ErrReleaseJobNotFound     = errors.New("release job not found")
	ErrReleaseJobTransition   = errors.New("release job transition is invalid")
)

var _ versioningrelease.IdempotentJobRepository = (*Store)(nil)
var _ versioningrelease.DurableJobRepository = (*Store)(nil)
var _ versioningrelease.CancellationIntentRepository = (*Store)(nil)
var _ versioningrelease.ActiveJobReader = (*Store)(nil)

// CreateOrGetReleaseJob atomically implements project-scoped idempotency.
// No intent, backup, or external activation is created here: it only records
// the queued durable Job after synchronous preflight has accepted the request.
func (s *Store) CreateOrGetReleaseJob(ctx context.Context, request versioningrelease.JobRequest) (versioningrelease.Job, bool, error) {
	if !request.Valid() {
		return versioningrelease.Job{}, false, ErrReleaseJobInvalid
	}
	if request.ProjectID != s.projectID {
		return versioningrelease.Job{}, false, ErrReleaseJobProjectScope
	}
	record, replay, err := s.CreateOrGet(ctx, sharedjob.Request{ProjectID: request.ProjectID, Kind: versioningrelease.SharedJobKind, RevisionID: request.RevisionID, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash})
	if errors.Is(err, ErrJobIdempotencyConflict) {
		return versioningrelease.Job{}, false, ErrIdempotencyConflict
	}
	if err != nil {
		return versioningrelease.Job{}, false, err
	}
	job, err := versioningrelease.JobFromSharedRecord(record)
	return job, replay, err
}

func findReleaseJobByKey(ctx context.Context, tx *sql.Tx, projectID domain.ID, key string) (versioningrelease.Job, bool, error) {
	job, err := scanReleaseJob(tx.QueryRowContext(ctx, `SELECT id,project_uuid,revision_id,input_hash,idempotency_key,request_hash,status,result_type,result_id,result_url,created_at,updated_at FROM jobs WHERE project_uuid=? AND idempotency_key=?`, projectID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return versioningrelease.Job{}, false, nil
	}
	if err != nil {
		return versioningrelease.Job{}, false, err
	}
	return job, true, nil
}

func (s *Store) GetReleaseJob(ctx context.Context, jobID domain.ID) (versioningrelease.Job, error) {
	if !jobID.Valid() {
		return versioningrelease.Job{}, ErrReleaseJobNotFound
	}
	record, err := s.GetJob(ctx, jobID)
	if errors.Is(err, ErrJobNotFound) {
		return versioningrelease.Job{}, ErrReleaseJobNotFound
	}
	if err != nil {
		return versioningrelease.Job{}, err
	}
	return versioningrelease.JobFromSharedRecord(record)
}

func (s *Store) RequestReleaseCancellation(ctx context.Context, jobID domain.ID) (versioningrelease.Job, bool, error) {
	record, replay, err := s.RequestCancellation(ctx, jobID)
	if errors.Is(err, ErrJobNotFound) {
		return versioningrelease.Job{}, false, ErrReleaseJobNotFound
	}
	if err != nil {
		return versioningrelease.Job{}, false, err
	}
	job, err := versioningrelease.JobFromSharedRecord(record)
	return job, replay, err
}

func (s *Store) HasActiveReleaseJob(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE project_uuid=? AND kind='release' AND status IN ('queued','running')`, s.projectID).Scan(&count)
	return count > 0, err
}

func (s *Store) TransitionReleaseJob(ctx context.Context, jobID domain.ID, expected, next versioningrelease.JobStatus, result *versioningrelease.JobResult) (versioningrelease.Job, bool, error) {
	if !jobID.Valid() || !expected.Valid() || !next.Valid() || !expected.CanTransitionTo(next) || (result != nil && !result.Valid()) || (next == versioningrelease.JobSucceeded && result == nil) {
		return versioningrelease.Job{}, false, ErrReleaseJobTransition
	}
	current, err := s.GetJob(ctx, jobID)
	if errors.Is(err, ErrJobNotFound) {
		return versioningrelease.Job{}, false, ErrReleaseJobNotFound
	}
	if err != nil || current.Kind != versioningrelease.SharedJobKind {
		return versioningrelease.Job{}, false, ErrReleaseJobTransition
	}
	var sharedResult *sharedjob.Result
	if result != nil {
		sharedResult = &sharedjob.Result{Type: result.Type, ID: result.ID, URL: result.URL}
	}
	record, swapped, err := s.Transition(ctx, jobID, sharedjob.Status(expected), sharedjob.Status(next), sharedResult, current.CancelGeneration)
	if err != nil || !swapped {
		return versioningrelease.Job{}, swapped, err
	}
	job, err := versioningrelease.JobFromSharedRecord(record)
	return job, true, err
}

type releaseJobScanner interface{ Scan(...any) error }

func scanReleaseJob(scanner releaseJobScanner) (versioningrelease.Job, error) {
	var id, projectID, revisionID, inputHash, key, requestHash, status, createdRaw, updatedRaw string
	var resultType, resultID, resultURL sql.NullString
	if err := scanner.Scan(&id, &projectID, &revisionID, &inputHash, &key, &requestHash, &status, &resultType, &resultID, &resultURL, &createdRaw, &updatedRaw); err != nil {
		return versioningrelease.Job{}, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdRaw)
	if err != nil {
		return versioningrelease.Job{}, err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, updatedRaw)
	if err != nil {
		return versioningrelease.Job{}, err
	}
	job := versioningrelease.Job{ID: domain.ID(id), ProjectID: domain.ID(projectID), RevisionID: domain.ID(revisionID), InputHash: inputHash, IdempotencyKey: key, RequestHash: requestHash, Status: versioningrelease.JobStatus(status), CreatedAt: createdAt, UpdatedAt: updatedAt}
	if resultType.Valid || resultID.Valid || resultURL.Valid {
		if !resultType.Valid || !resultID.Valid || !resultURL.Valid {
			return versioningrelease.Job{}, errors.New("partial stored release job result")
		}
		job.Result = &versioningrelease.JobResult{Type: resultType.String, ID: domain.ID(resultID.String), URL: resultURL.String}
	}
	if !job.Valid() {
		return versioningrelease.Job{}, errors.New("invalid stored release job")
	}
	return job, nil
}
