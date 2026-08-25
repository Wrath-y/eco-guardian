package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/risk/orchestration"
)

var _ orchestration.MaterializationStore = (*Store)(nil)

func (s *Store) SaveRiskJobMaterialization(ctx context.Context, materialization orchestration.Materialization) error {
	if !materialization.Valid() || materialization.ProjectID != s.projectID {
		return ErrRiskReportInvalid
	}
	evidence, err := json.Marshal(materialization.ImpactEvidence)
	if err != nil {
		return err
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	existing, err := getRiskJobMaterialization(ctx, tx, materialization.JobID, s.projectID)
	if err == nil {
		if !sameRiskMaterialization(existing, materialization) {
			return ErrRiskReportReplay
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	write, err := tx.ExecContext(ctx, `INSERT INTO risk_job_materializations(job_id,project_uuid,candidate_revision_id,canonical_input,input_hash,canonical_evidence_refs,evidence_hash,request_hash,cancel_generation,created_at) SELECT ?,?,?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM jobs WHERE id=? AND project_uuid=? AND kind=? AND revision_id=? AND input_hash=? AND request_hash=? AND cancel_generation=?)`, materialization.JobID, materialization.ProjectID, materialization.CandidateRevision, string(materialization.CanonicalInput), materialization.InputHash, string(evidence), materialization.EvidenceHash, materialization.RequestHash, materialization.CancelGeneration, materialization.CreatedAt.UTC().Format(time.RFC3339Nano), materialization.JobID, materialization.ProjectID, orchestration.RiskReviewJobKind, materialization.CandidateRevision, materialization.InputHash, materialization.RequestHash, materialization.CancelGeneration)
	if err != nil {
		return err
	}
	if rows, _ := write.RowsAffected(); rows != 1 {
		return ErrRiskReportInvalid
	}
	return tx.Commit()
}

func (s *Store) GetRiskJobMaterialization(ctx context.Context, jobID domain.ID) (orchestration.Materialization, error) {
	if !jobID.Valid() {
		return orchestration.Materialization{}, ErrRiskReportInvalid
	}
	value, err := getRiskJobMaterialization(ctx, s.db, jobID, s.projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return orchestration.Materialization{}, ErrNotFound
	}
	return value, err
}

type riskMaterializationQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getRiskJobMaterialization(ctx context.Context, query riskMaterializationQuerier, jobID, projectID domain.ID) (orchestration.Materialization, error) {
	var value orchestration.Materialization
	var inputRaw, evidenceRaw, createdAt string
	if err := query.QueryRowContext(ctx, `SELECT job_id,project_uuid,candidate_revision_id,canonical_input,input_hash,canonical_evidence_refs,evidence_hash,request_hash,cancel_generation,created_at FROM risk_job_materializations WHERE job_id=? AND project_uuid=?`, jobID, projectID).Scan(&value.JobID, &value.ProjectID, &value.CandidateRevision, &inputRaw, &value.InputHash, &evidenceRaw, &value.EvidenceHash, &value.RequestHash, &value.CancelGeneration, &createdAt); err != nil {
		return orchestration.Materialization{}, err
	}
	value.CanonicalInput = []byte(inputRaw)
	if err := strictRiskJSON(value.CanonicalInput, &value.Input); err != nil {
		return orchestration.Materialization{}, err
	}
	if err := strictRiskJSON([]byte(evidenceRaw), &value.ImpactEvidence); err != nil {
		return orchestration.Materialization{}, err
	}
	parsedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return orchestration.Materialization{}, err
	}
	value.CreatedAt = parsedAt
	if !value.Valid() {
		return orchestration.Materialization{}, ErrRiskReportInvalid
	}
	return value, nil
}

func (s *Store) ListRecoverableRiskJobs(ctx context.Context) ([]sharedjob.Record, error) {
	rows, err := s.db.QueryContext(ctx, sharedJobSelect+` WHERE project_uuid=? AND kind=? AND status IN ('queued','running','interrupted') ORDER BY created_at,id`, s.projectID, orchestration.RiskReviewJobKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []sharedjob.Record{}
	for rows.Next() {
		job, scanErr := scanSharedJob(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func sameRiskMaterialization(left, right orchestration.Materialization) bool {
	return left.JobID == right.JobID && left.ProjectID == right.ProjectID && left.CandidateRevision == right.CandidateRevision && left.InputHash == right.InputHash && left.EvidenceHash == right.EvidenceHash && left.RequestHash == right.RequestHash && left.CancelGeneration == right.CancelGeneration && left.CreatedAt.Equal(right.CreatedAt) && string(left.CanonicalInput) == string(right.CanonicalInput)
}
