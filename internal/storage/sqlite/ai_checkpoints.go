package sqlite

import (
	"context"
	"database/sql"
	"errors"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var _ aiorchestration.DeterministicCheckpointRepository = (*Store)(nil)

func (s *Store) SaveDeterministicCheckpoint(ctx context.Context, checkpoint aiorchestration.DeterministicCheckpoint) (bool, error) {
	if ctx == nil || !checkpoint.Valid() {
		return false, aiorchestration.ErrAIRecoveryInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	state, err := loadAIJobState(ctx, tx, checkpoint.JobID, s.projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrAIJobStoreNotFound
	}
	if err != nil {
		return false, err
	}
	if checkpoint.InputHash != aicontract.Hash(state.Job.InputHash) || checkpoint.CancelGeneration != state.Job.CancelGeneration || checkpoint.Phase.Order() > state.Phase.Order() {
		return false, aiorchestration.ErrAIRecoveryConflict
	}

	current, found, err := loadDeterministicCheckpoint(ctx, tx, checkpoint.JobID, s.projectID, state.Job.CancelGeneration)
	if err != nil {
		return false, err
	}
	if found && current == checkpoint {
		return true, tx.Commit()
	}
	if state.Job.Status != sharedjob.Running || state.Owner == "" || found && checkpoint.Phase.Order() <= current.Phase.Order() {
		return false, aiorchestration.ErrAIRecoveryConflict
	}

	now := s.now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE ai_design_runs
		SET checkpoint_phase=?,checkpoint_input_hash=?,checkpoint_evidence_hash=?,checkpoint_dependency_hash=?,checkpoint_output_hash=?,updated_at=?
		WHERE job_id=? AND project_uuid=? AND checkpoint_phase IS ?`,
		checkpoint.Phase, checkpoint.InputHash, checkpoint.EvidenceManifestHash, checkpoint.DependencyHash, checkpoint.OutputHash, formatAIJobTime(now),
		checkpoint.JobID, s.projectID, nullableCheckpointPhase(current, found))
	if err != nil {
		return false, err
	}
	if changed, rowsErr := result.RowsAffected(); rowsErr != nil || changed != 1 {
		return false, aiorchestration.ErrAIRecoveryConflict
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Store) LoadDeterministicCheckpoint(ctx context.Context, jobID domain.ID) (aiorchestration.DeterministicCheckpoint, bool, error) {
	if ctx == nil || !jobID.Valid() {
		return aiorchestration.DeterministicCheckpoint{}, false, aiorchestration.ErrAIRecoveryInvalid
	}
	state, err := s.GetAIJob(ctx, jobID)
	if err != nil {
		return aiorchestration.DeterministicCheckpoint{}, false, err
	}
	return loadDeterministicCheckpoint(ctx, s.db, jobID, s.projectID, state.Job.CancelGeneration)
}

type aiCheckpointQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadDeterministicCheckpoint(ctx context.Context, queryer aiCheckpointQueryer, jobID, projectID domain.ID, cancelGeneration int64) (aiorchestration.DeterministicCheckpoint, bool, error) {
	var phase, inputHash, evidenceHash, dependencyHash, outputHash sql.NullString
	err := queryer.QueryRowContext(ctx, `SELECT checkpoint_phase,checkpoint_input_hash,checkpoint_evidence_hash,checkpoint_dependency_hash,checkpoint_output_hash
		FROM ai_design_runs WHERE job_id=? AND project_uuid=?`, jobID, projectID).Scan(&phase, &inputHash, &evidenceHash, &dependencyHash, &outputHash)
	if err != nil {
		return aiorchestration.DeterministicCheckpoint{}, false, err
	}
	if !phase.Valid {
		return aiorchestration.DeterministicCheckpoint{}, false, nil
	}
	checkpoint := aiorchestration.DeterministicCheckpoint{
		DeterministicRecoveryIdentity: aiorchestration.DeterministicRecoveryIdentity{
			JobID: jobID, Phase: aiorchestration.JobPhase(phase.String), InputHash: aicontract.Hash(inputHash.String),
			EvidenceManifestHash: aicontract.Hash(evidenceHash.String), DependencyHash: aicontract.Hash(dependencyHash.String), CancelGeneration: cancelGeneration,
		},
		OutputHash: aicontract.Hash(outputHash.String),
	}
	if !checkpoint.Valid() {
		return aiorchestration.DeterministicCheckpoint{}, false, aiorchestration.ErrAIRecoveryInvalid
	}
	return checkpoint, true, nil
}

func nullableCheckpointPhase(checkpoint aiorchestration.DeterministicCheckpoint, found bool) any {
	if !found {
		return nil
	}
	return checkpoint.Phase
}
