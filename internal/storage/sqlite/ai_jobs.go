package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var (
	ErrAIJobStoreInvalid  = errors.New("AI Job persistence request is invalid")
	ErrAIJobStoreNotFound = errors.New("AI Job persistence record was not found")
	ErrAIJobStoreConflict = errors.New("AI Job persistence conflict")
)

var _ aiorchestration.AIJobRepository = (*Store)(nil)

func (s *Store) AdmitAIJob(ctx context.Context, admission aiorchestration.AIJobAdmission) (aiorchestration.AIJobState, bool, error) {
	if ctx == nil || !admission.Valid() || admission.Request.ProjectID != s.projectID {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	defer tx.Rollback()
	if existing, found, findErr := findSharedJobByKey(ctx, tx, s.projectID, admission.Request.IdempotencyKey); findErr != nil {
		return aiorchestration.AIJobState{}, false, findErr
	} else if found {
		if !existing.Request().Equivalent(admission.Request) || existing.Kind != aiorchestration.AIJobKind {
			return aiorchestration.AIJobState{}, false, ErrJobIdempotencyConflict
		}
		state, stateErr := loadAIJobState(ctx, tx, existing.ID, s.projectID)
		if stateErr != nil {
			return aiorchestration.AIJobState{}, false, stateErr
		}
		return state, true, tx.Commit()
	}
	id, err := domain.NewID()
	if err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	now := s.now().UTC()
	job := sharedjob.Record{
		ID: id, ProjectID: admission.Request.ProjectID, Kind: admission.Request.Kind, RevisionID: admission.Request.RevisionID,
		InputHash: admission.Request.InputHash, IdempotencyKey: admission.Request.IdempotencyKey, RequestHash: admission.Request.RequestHash,
		Status: sharedjob.Queued, CreatedAt: now, UpdatedAt: now,
	}
	state := aiorchestration.AIJobState{Job: job, Phase: admission.Phase}
	if !state.Valid() {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreInvalid
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO jobs(id,project_uuid,kind,revision_id,input_hash,idempotency_key,request_hash,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, job.ID, job.ProjectID, job.Kind, job.RevisionID, job.InputHash, job.IdempotencyKey, job.RequestHash, job.Status, formatAIJobTime(now), formatAIJobTime(now)); err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_design_runs(job_id,project_uuid,base_revision_id,input_hash,canonical_input,phase,owner,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, job.ID, job.ProjectID, job.RevisionID, job.InputHash, admission.CanonicalInput, state.Phase, nil, formatAIJobTime(now), formatAIJobTime(now)); err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	return state, false, nil
}

func (s *Store) GetAIJob(ctx context.Context, jobID domain.ID) (aiorchestration.AIJobState, error) {
	if ctx == nil || !jobID.Valid() {
		return aiorchestration.AIJobState{}, ErrAIJobStoreNotFound
	}
	state, err := loadAIJobState(ctx, s.db, jobID, s.projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return aiorchestration.AIJobState{}, ErrAIJobStoreNotFound
	}
	return state, err
}

func (s *Store) RequestAIJobCancellation(ctx context.Context, jobID domain.ID) (aiorchestration.AIJobState, bool, error) {
	if ctx == nil || !jobID.Valid() {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	defer tx.Rollback()
	current, err := loadAIJobState(ctx, tx, jobID, s.projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreNotFound
	}
	if err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	if current.Job.CancelGeneration > 0 {
		return current, true, tx.Commit()
	}
	if current.Job.Status == sharedjob.Succeeded || current.Job.Status == sharedjob.Failed || current.Job.Status == sharedjob.Canceled {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreConflict
	}
	nextStatus := current.Job.Status
	nextOwner := current.Owner
	if current.Job.Status == sharedjob.Queued || current.Job.Status == sharedjob.Interrupted {
		nextStatus = sharedjob.Canceled
		nextOwner = ""
	}
	now := s.now().UTC()
	write, err := tx.ExecContext(ctx, `UPDATE jobs SET status=?,cancel_generation=cancel_generation+1,cancel_requested_at=?,updated_at=? WHERE id=? AND project_uuid=? AND kind=? AND status=? AND cancel_generation=0`, nextStatus, formatAIJobTime(now), formatAIJobTime(now), jobID, s.projectID, aiorchestration.AIJobKind, current.Job.Status)
	if err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	if changed, rowsErr := write.RowsAffected(); rowsErr != nil || changed != 1 {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreConflict
	}
	runWrite, err := tx.ExecContext(ctx, `UPDATE ai_design_runs SET owner=?,updated_at=? WHERE job_id=? AND project_uuid=? AND phase=? AND owner IS ?`, nullableAIJobOwner(nextOwner), formatAIJobTime(now), jobID, s.projectID, current.Phase, nullableAIJobOwner(current.Owner))
	if err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	if changed, rowsErr := runWrite.RowsAffected(); rowsErr != nil || changed != 1 {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreConflict
	}
	if err = tx.Commit(); err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	state, err := s.GetAIJob(ctx, jobID)
	return state, false, err
}

func (s *Store) TransitionAIJob(ctx context.Context, transition aiorchestration.AIJobTransition) (aiorchestration.AIJobState, bool, error) {
	if ctx == nil || !transition.Valid() {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	defer tx.Rollback()
	current, err := loadAIJobState(ctx, tx, transition.JobID, s.projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreNotFound
	}
	if err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	if current.Job.Status == transition.NextStatus && current.Phase == transition.NextPhase && current.Owner == transition.NextOwner && sameAIStoredResult(current.Job.Result, transition.Result) && current.Job.CancelGeneration == transition.ObservedCancelGeneration {
		return current, true, tx.Commit()
	}
	if current.Job.Status != transition.ExpectedStatus || current.Phase != transition.ExpectedPhase || current.Owner != transition.ExpectedOwner || current.Job.CancelGeneration != transition.ObservedCancelGeneration || !validAIStoredTransition(current.Job.Status, transition.NextStatus) {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreConflict
	}
	if transition.NextStatus == sharedjob.Succeeded && transition.ObservedCancelGeneration != 0 {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreConflict
	}
	var resultType, resultID, resultURL any
	if transition.Result != nil {
		resultType, resultID, resultURL = transition.Result.Type, transition.Result.ID, transition.Result.URL
	}
	now := s.now().UTC()
	jobWrite, err := tx.ExecContext(ctx, `UPDATE jobs SET status=?,result_type=?,result_id=?,result_url=?,updated_at=? WHERE id=? AND project_uuid=? AND kind=? AND status=? AND cancel_generation=?`, transition.NextStatus, resultType, resultID, resultURL, formatAIJobTime(now), transition.JobID, s.projectID, aiorchestration.AIJobKind, transition.ExpectedStatus, transition.ObservedCancelGeneration)
	if err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	if changed, rowsErr := jobWrite.RowsAffected(); rowsErr != nil || changed != 1 {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreConflict
	}
	runWrite, err := tx.ExecContext(ctx, `UPDATE ai_design_runs SET phase=?,owner=?,updated_at=? WHERE job_id=? AND project_uuid=? AND phase=? AND owner IS ?`, transition.NextPhase, nullableAIJobOwner(transition.NextOwner), formatAIJobTime(now), transition.JobID, s.projectID, transition.ExpectedPhase, nullableAIJobOwner(transition.ExpectedOwner))
	if err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	if changed, rowsErr := runWrite.RowsAffected(); rowsErr != nil || changed != 1 {
		return aiorchestration.AIJobState{}, false, ErrAIJobStoreConflict
	}
	if err = tx.Commit(); err != nil {
		return aiorchestration.AIJobState{}, false, err
	}
	state, err := s.GetAIJob(ctx, transition.JobID)
	return state, false, err
}

type aiJobStateQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadAIJobState(ctx context.Context, queryer aiJobStateQueryer, jobID, projectID domain.ID) (aiorchestration.AIJobState, error) {
	job, err := scanSharedJob(queryer.QueryRowContext(ctx, sharedJobSelect+` WHERE id=? AND project_uuid=? AND kind=?`, jobID, projectID, aiorchestration.AIJobKind))
	if err != nil {
		return aiorchestration.AIJobState{}, err
	}
	var phase string
	var owner sql.NullString
	if err = queryer.QueryRowContext(ctx, `SELECT phase,owner FROM ai_design_runs WHERE job_id=? AND project_uuid=?`, jobID, projectID).Scan(&phase, &owner); err != nil {
		return aiorchestration.AIJobState{}, err
	}
	state := aiorchestration.AIJobState{Job: job, Phase: aiorchestration.JobPhase(phase), Owner: owner.String}
	if !state.Valid() {
		return aiorchestration.AIJobState{}, ErrAIJobStoreInvalid
	}
	return state, nil
}

func validAIStoredTransition(current, next sharedjob.Status) bool {
	return current == next && current == sharedjob.Running || current.CanTransitionTo(next)
}

func sameAIStoredResult(left, right *sharedjob.Result) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func nullableAIJobOwner(owner string) any {
	if owner == "" {
		return nil
	}
	return owner
}

func formatAIJobTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
