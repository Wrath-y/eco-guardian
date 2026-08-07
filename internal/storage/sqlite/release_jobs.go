package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
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
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return versioningrelease.Job{}, false, err
	}
	defer tx.Rollback()
	if existing, found, err := findReleaseJobByKey(ctx, tx, request.ProjectID, request.IdempotencyKey); err != nil {
		return versioningrelease.Job{}, false, err
	} else if found {
		if existing.RequestHash != request.RequestHash {
			return versioningrelease.Job{}, false, ErrIdempotencyConflict
		}
		return existing, true, tx.Commit()
	}
	id, err := domain.NewID()
	if err != nil {
		return versioningrelease.Job{}, false, err
	}
	createdAt := s.now().UTC()
	job := versioningrelease.Job{ID: id, ProjectID: request.ProjectID, RevisionID: request.RevisionID, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, Status: versioningrelease.JobQueued, CreatedAt: createdAt, UpdatedAt: createdAt}
	if !job.Valid() {
		return versioningrelease.Job{}, false, ErrReleaseJobInvalid
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, job.ID, job.ProjectID, "release", job.RevisionID, job.InputHash, job.IdempotencyKey, job.RequestHash, job.Status, createdAt.Format(time.RFC3339Nano), createdAt.Format(time.RFC3339Nano))
	if err != nil {
		return versioningrelease.Job{}, false, fmt.Errorf("insert release job: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return versioningrelease.Job{}, false, err
	}
	return job, false, nil
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
	job, err := scanReleaseJob(s.db.QueryRowContext(ctx, `SELECT id,project_uuid,revision_id,input_hash,idempotency_key,request_hash,status,result_type,result_id,result_url,created_at,updated_at FROM jobs WHERE id=? AND project_uuid=?`, jobID, s.projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return versioningrelease.Job{}, ErrReleaseJobNotFound
	}
	return job, err
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
	updatedAt := s.now().UTC()
	var resultType, resultID, resultURL any
	if result != nil {
		resultType, resultID, resultURL = result.Type, result.ID, result.URL
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	write, err := s.db.ExecContext(ctx, `UPDATE jobs SET status=?,result_type=?,result_id=?,result_url=?,updated_at=? WHERE id=? AND project_uuid=? AND status=?`, next, resultType, resultID, resultURL, updatedAt.Format(time.RFC3339Nano), jobID, s.projectID, expected)
	if err != nil {
		return versioningrelease.Job{}, false, err
	}
	swapped, err := write.RowsAffected()
	if err != nil {
		return versioningrelease.Job{}, false, err
	}
	if swapped == 0 {
		return versioningrelease.Job{}, false, nil
	}
	job, err := s.GetReleaseJob(ctx, jobID)
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
