package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
)

var (
	ErrSimulationRunInvalid = errors.New("simulation run is invalid")
	ErrSimulationSeal       = errors.New("simulation run cannot be sealed")
	ErrSimulationFailure    = errors.New("simulation job cannot be failed")
)

type SimulationRun struct {
	ID, JobID, ProjectID, RevisionID, ScenarioDefinitionID  domain.ID
	InputHash, FingerprintHash, ResultHash, CanonicalResult string
	CreatedAt                                               time.Time
}

// FailSimulationJob atomically persists the terminal Job failure and its
// diagnostic event. It never writes a run, metric, or checkpoint fact.
func (s *Store) FailSimulationJob(ctx context.Context, jobID domain.ID, cancelGeneration int64, code, detail string) (sharedjob.Record, bool, error) {
	if !jobID.Valid() || cancelGeneration < 0 || (code != "BUDGET_EXCEEDED" && code != "TIMEOUT" && code != "RECOVERY_MISMATCH" && code != "RECOVERY_UNAVAILABLE") || detail == "" || len(code)+len(detail)+2 > 1024 {
		return sharedjob.Record{}, false, ErrSimulationFailure
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	defer tx.Rollback()
	record, err := scanSharedJob(tx.QueryRowContext(ctx, sharedJobSelect+` WHERE id=? AND project_uuid=?`, jobID, s.projectID))
	if err != nil || record.Kind != "simulation" || record.CancelGeneration != cancelGeneration || record.CancelGeneration != 0 {
		return sharedjob.Record{}, false, ErrSimulationFailure
	}
	if record.Status == sharedjob.Failed {
		var stored sql.NullString
		if err = tx.QueryRowContext(ctx, `SELECT error FROM job_events WHERE job_id=? ORDER BY event_ordinal DESC LIMIT 1`, jobID).Scan(&stored); err != nil || !stored.Valid || stored.String != code+": "+detail {
			return sharedjob.Record{}, false, ErrSimulationFailure
		}
		return record, true, tx.Commit()
	}
	if record.Status.Terminal() || (record.Status != sharedjob.Queued && record.Status != sharedjob.Running && record.Status != sharedjob.Interrupted) {
		return sharedjob.Record{}, false, ErrSimulationFailure
	}
	now := s.now().UTC()
	write, err := tx.ExecContext(ctx, `UPDATE jobs SET status='failed',updated_at=? WHERE id=? AND project_uuid=? AND kind='simulation' AND status=? AND cancel_generation=0`, now.Format(time.RFC3339Nano), jobID, s.projectID, record.Status)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	if changed, _ := write.RowsAffected(); changed != 1 {
		return sharedjob.Record{}, false, ErrSimulationFailure
	}
	var lastOrdinal int64
	var progress int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(event_ordinal),0),COALESCE(MAX(progress),0) FROM job_events WHERE job_id=?`, jobID).Scan(&lastOrdinal, &progress); err != nil {
		return sharedjob.Record{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_events(job_id,event_ordinal,phase,progress,error,created_at) VALUES(?,?,?,?,?,?)`, jobID, lastOrdinal+1, "FAILED", progress, code+": "+detail, now.Format(time.RFC3339Nano)); err != nil {
		return sharedjob.Record{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return sharedjob.Record{}, false, err
	}
	updated, err := s.GetJob(ctx, jobID)
	return updated, false, err
}

type SimulationMetricResult struct{ MetricID, MetricVersion, Status, CanonicalResult string }
type SimulationVerification struct {
	ID, SourceRunID, ReproductionRunID                                           domain.ID
	InputHash, FingerprintHash, SourceResultHash, ReproductionResultHash, Status string
	CreatedAt                                                                    time.Time
}
type SimulationCheckpoint struct {
	JobID                        domain.ID
	SampleOrdinal                uint64
	InputHash, FingerprintHash   string
	CancelGeneration             int64
	Accumulator, AccumulatorHash string
	CompletedAt                  time.Time
}
type SimulationJobMaterialization struct {
	JobID, ProjectID, RevisionID, ScenarioDefinitionID domain.ID
	CanonicalInput                                     []byte
	InputHash, FingerprintHash                         string
	CancelGeneration                                   int64
	CreatedAt                                          time.Time
}

func (run SimulationRun) valid() bool {
	return run.ID.Valid() && run.JobID.Valid() && run.ProjectID.Valid() && run.RevisionID.Valid() && run.ScenarioDefinitionID.Valid() && validSimulationHash(run.InputHash) && validSimulationHash(run.FingerprintHash) && validSimulationHash(run.ResultHash) && run.CanonicalResult != "" && !run.CreatedAt.IsZero()
}

func (materialization SimulationJobMaterialization) valid() bool {
	return materialization.JobID.Valid() && materialization.ProjectID.Valid() && materialization.RevisionID.Valid() && materialization.ScenarioDefinitionID.Valid() && len(materialization.CanonicalInput) > 0 && validSimulationHash(materialization.InputHash) && validSimulationHash(materialization.FingerprintHash) && materialization.CancelGeneration >= 0 && !materialization.CreatedAt.IsZero() && hashSimulationBytes(materialization.CanonicalInput) == materialization.InputHash
}

func SimulationAccumulatorHash(accumulator string) string {
	return contract.CheckpointAccumulatorHash(accumulator)
}

func hashSimulationBytes(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

// SaveSimulationJobMaterialization records the complete captured identity
// before execution. A materialization is immutable and must agree with the
// shared Job's project, revision, and input hash.
func (s *Store) SaveSimulationJobMaterialization(ctx context.Context, materialization SimulationJobMaterialization) error {
	if !materialization.valid() || materialization.ProjectID != s.projectID {
		return ErrSimulationRunInvalid
	}
	write, err := s.db.ExecContext(ctx, `INSERT INTO simulation_job_materializations(job_id,project_uuid,revision_id,scenario_definition_id,canonical_input,input_hash,fingerprint_hash,cancel_generation,created_at) SELECT ?,?,?,?,?,?,?,?,? WHERE EXISTS (SELECT 1 FROM jobs WHERE id=? AND project_uuid=? AND kind='simulation' AND revision_id=? AND input_hash=?)`, materialization.JobID, materialization.ProjectID, materialization.RevisionID, materialization.ScenarioDefinitionID, materialization.CanonicalInput, materialization.InputHash, materialization.FingerprintHash, materialization.CancelGeneration, materialization.CreatedAt.UTC().Format(time.RFC3339Nano), materialization.JobID, materialization.ProjectID, materialization.RevisionID, materialization.InputHash)
	if err != nil {
		return err
	}
	if rows, _ := write.RowsAffected(); rows != 1 {
		return ErrSimulationRunInvalid
	}
	return nil
}

func (s *Store) GetSimulationJobMaterialization(ctx context.Context, jobID domain.ID) (SimulationJobMaterialization, error) {
	var materialization SimulationJobMaterialization
	var createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT job_id,project_uuid,revision_id,scenario_definition_id,canonical_input,input_hash,fingerprint_hash,cancel_generation,created_at FROM simulation_job_materializations WHERE job_id=? AND project_uuid=?`, jobID, s.projectID).Scan(&materialization.JobID, &materialization.ProjectID, &materialization.RevisionID, &materialization.ScenarioDefinitionID, &materialization.CanonicalInput, &materialization.InputHash, &materialization.FingerprintHash, &materialization.CancelGeneration, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SimulationJobMaterialization{}, ErrNotFound
	}
	if err != nil {
		return SimulationJobMaterialization{}, err
	}
	if materialization.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil || !materialization.valid() {
		return SimulationJobMaterialization{}, ErrSimulationRunInvalid
	}
	return materialization, nil
}

// ListRecoverableSimulationJobs is the startup/project-reopen scan. It never
// returns terminal Jobs, and callers must still validate materialization and
// checkpoint identities before resuming any work.
func (s *Store) ListRecoverableSimulationJobs(ctx context.Context) ([]sharedjob.Record, error) {
	rows, err := s.db.QueryContext(ctx, sharedJobSelect+` WHERE project_uuid=? AND kind='simulation' AND status IN ('queued','running','interrupted') ORDER BY created_at,id`, s.projectID)
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

func (s *Store) InsertSimulationRun(ctx context.Context, run SimulationRun, metrics []SimulationMetricResult) error {
	if !run.valid() || run.ProjectID != s.projectID || len(metrics) == 0 {
		return ErrSimulationRunInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO simulation_runs(id,job_id,project_uuid,revision_id,scenario_definition_id,input_hash,fingerprint_hash,result_hash,canonical_result,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, run.ID, run.JobID, run.ProjectID, run.RevisionID, run.ScenarioDefinitionID, run.InputHash, run.FingerprintHash, run.ResultHash, run.CanonicalResult, run.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	for _, metric := range metrics {
		if metric.MetricID == "" || metric.MetricVersion == "" || (metric.Status != "available" && metric.Status != "unavailable") || metric.CanonicalResult == "" {
			return ErrSimulationRunInvalid
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO simulation_metric_results(run_id,metric_id,metric_version,status,canonical_result) VALUES(?,?,?,?,?)`, run.ID, metric.MetricID, metric.MetricVersion, metric.Status, metric.CanonicalResult); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SealSimulationRun makes the immutable run visible only after every required
// sample checkpoint matches the captured Job identity and cancellation
// generation. The run facts and shared Job success result commit in one short
// transaction.
func (s *Store) SealSimulationRun(ctx context.Context, run SimulationRun, metrics []SimulationMetricResult, requiredSamples int, cancelGeneration int64) error {
	if !run.valid() || run.ProjectID != s.projectID || len(metrics) == 0 || requiredSamples < 1 || cancelGeneration < 0 {
		return ErrSimulationSeal
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var kind, status, inputHash, revisionID string
	var generation int64
	if err = tx.QueryRowContext(ctx, `SELECT kind,status,cancel_generation,input_hash,revision_id FROM jobs WHERE id=? AND project_uuid=?`, run.JobID, s.projectID).Scan(&kind, &status, &generation, &inputHash, &revisionID); err != nil {
		return ErrSimulationSeal
	}
	if kind != "simulation" || status != "running" || generation != cancelGeneration || inputHash != run.InputHash || revisionID != string(run.RevisionID) {
		return ErrSimulationSeal
	}
	var checkpointCount int
	var firstOrdinal, lastOrdinal sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),MIN(sample_ordinal),MAX(sample_ordinal) FROM simulation_job_checkpoints WHERE job_id=? AND input_hash=? AND fingerprint_hash=? AND cancel_generation=?`, run.JobID, run.InputHash, run.FingerprintHash, cancelGeneration).Scan(&checkpointCount, &firstOrdinal, &lastOrdinal); err != nil {
		return err
	}
	if checkpointCount != requiredSamples || !firstOrdinal.Valid || !lastOrdinal.Valid || firstOrdinal.Int64 != 0 || lastOrdinal.Int64 != int64(requiredSamples-1) {
		return ErrSimulationSeal
	}
	if err = insertSimulationRun(ctx, tx, run, metrics); err != nil {
		return err
	}
	write, err := tx.ExecContext(ctx, `UPDATE jobs SET status='succeeded',result_type='simulation_run',result_id=?,result_url=?,updated_at=? WHERE id=? AND project_uuid=? AND status='running' AND cancel_generation=?`, run.ID, "/api/v1/simulation-runs/"+string(run.ID), s.now().UTC().Format(time.RFC3339Nano), run.JobID, s.projectID, cancelGeneration)
	if err != nil {
		return err
	}
	if rows, _ := write.RowsAffected(); rows != 1 {
		return ErrSimulationSeal
	}
	return tx.Commit()
}

func insertSimulationRun(ctx context.Context, tx *sql.Tx, run SimulationRun, metrics []SimulationMetricResult) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO simulation_runs(id,job_id,project_uuid,revision_id,scenario_definition_id,input_hash,fingerprint_hash,result_hash,canonical_result,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, run.ID, run.JobID, run.ProjectID, run.RevisionID, run.ScenarioDefinitionID, run.InputHash, run.FingerprintHash, run.ResultHash, run.CanonicalResult, run.CreatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	for _, metric := range metrics {
		if metric.MetricID == "" || metric.MetricVersion == "" || (metric.Status != "available" && metric.Status != "unavailable") || metric.CanonicalResult == "" {
			return ErrSimulationRunInvalid
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO simulation_metric_results(run_id,metric_id,metric_version,status,canonical_result) VALUES(?,?,?,?,?)`, run.ID, metric.MetricID, metric.MetricVersion, metric.Status, metric.CanonicalResult); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetSimulationRun(ctx context.Context, id domain.ID) (SimulationRun, []SimulationMetricResult, error) {
	var run SimulationRun
	var createdAt string
	err := s.db.QueryRowContext(ctx, `SELECT id,job_id,project_uuid,revision_id,scenario_definition_id,input_hash,fingerprint_hash,result_hash,canonical_result,created_at FROM simulation_runs WHERE id=? AND project_uuid=?`, id, s.projectID).Scan(&run.ID, &run.JobID, &run.ProjectID, &run.RevisionID, &run.ScenarioDefinitionID, &run.InputHash, &run.FingerprintHash, &run.ResultHash, &run.CanonicalResult, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return SimulationRun{}, nil, ErrNotFound
	}
	if err != nil {
		return SimulationRun{}, nil, err
	}
	if run.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt); err != nil || !run.valid() {
		return SimulationRun{}, nil, ErrSimulationRunInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT metric_id,metric_version,status,canonical_result FROM simulation_metric_results WHERE run_id=? ORDER BY metric_id`, id)
	if err != nil {
		return SimulationRun{}, nil, err
	}
	defer rows.Close()
	metrics := []SimulationMetricResult{}
	for rows.Next() {
		var metric SimulationMetricResult
		if err = rows.Scan(&metric.MetricID, &metric.MetricVersion, &metric.Status, &metric.CanonicalResult); err != nil {
			return SimulationRun{}, nil, err
		}
		metrics = append(metrics, metric)
	}
	return run, metrics, rows.Err()
}

func (s *Store) SaveSimulationCheckpoint(ctx context.Context, checkpoint SimulationCheckpoint) error {
	if !checkpoint.JobID.Valid() || checkpoint.SampleOrdinal > uint64(^uint64(0)>>1) || !validSimulationHash(checkpoint.InputHash) || !validSimulationHash(checkpoint.FingerprintHash) || checkpoint.CancelGeneration < 0 || checkpoint.Accumulator == "" || checkpoint.AccumulatorHash != SimulationAccumulatorHash(checkpoint.Accumulator) || checkpoint.CompletedAt.IsZero() {
		return ErrSimulationRunInvalid
	}
	write, err := s.db.ExecContext(ctx, `INSERT INTO simulation_job_checkpoints(job_id,sample_ordinal,input_hash,fingerprint_hash,cancel_generation,accumulator,accumulator_hash,completed_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(job_id,sample_ordinal) DO UPDATE SET accumulator=excluded.accumulator,accumulator_hash=excluded.accumulator_hash,completed_at=excluded.completed_at WHERE simulation_job_checkpoints.input_hash=excluded.input_hash AND simulation_job_checkpoints.fingerprint_hash=excluded.fingerprint_hash AND simulation_job_checkpoints.cancel_generation=excluded.cancel_generation`, checkpoint.JobID, checkpoint.SampleOrdinal, checkpoint.InputHash, checkpoint.FingerprintHash, checkpoint.CancelGeneration, checkpoint.Accumulator, checkpoint.AccumulatorHash, checkpoint.CompletedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if rows, _ := write.RowsAffected(); rows != 1 {
		return fmt.Errorf("%w: checkpoint identity mismatch", ErrSimulationRunInvalid)
	}
	return nil
}

func (s *Store) ListSimulationCheckpoints(ctx context.Context, jobID domain.ID, inputHash, fingerprintHash string, cancelGeneration int64) ([]SimulationCheckpoint, error) {
	if !jobID.Valid() || !validSimulationHash(inputHash) || !validSimulationHash(fingerprintHash) || cancelGeneration < 0 {
		return nil, ErrSimulationRunInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT job_id,sample_ordinal,input_hash,fingerprint_hash,cancel_generation,accumulator,accumulator_hash,completed_at FROM simulation_job_checkpoints WHERE job_id=? AND input_hash=? AND fingerprint_hash=? AND cancel_generation=? ORDER BY sample_ordinal`, jobID, inputHash, fingerprintHash, cancelGeneration)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	checkpoints := []SimulationCheckpoint{}
	for rows.Next() {
		var checkpoint SimulationCheckpoint
		var ordinal int64
		var completedAt string
		if err = rows.Scan(&checkpoint.JobID, &ordinal, &checkpoint.InputHash, &checkpoint.FingerprintHash, &checkpoint.CancelGeneration, &checkpoint.Accumulator, &checkpoint.AccumulatorHash, &completedAt); err != nil {
			return nil, err
		}
		if ordinal < 0 {
			return nil, ErrSimulationRunInvalid
		}
		checkpoint.SampleOrdinal = uint64(ordinal)
		if checkpoint.CompletedAt, err = time.Parse(time.RFC3339Nano, completedAt); err != nil || checkpoint.Accumulator == "" || checkpoint.AccumulatorHash != SimulationAccumulatorHash(checkpoint.Accumulator) {
			return nil, ErrSimulationRunInvalid
		}
		checkpoints = append(checkpoints, checkpoint)
	}
	return checkpoints, rows.Err()
}

func (s *Store) InsertSimulationVerification(ctx context.Context, verification SimulationVerification) error {
	if !verification.ID.Valid() || !verification.SourceRunID.Valid() || !verification.ReproductionRunID.Valid() || !validSimulationHash(verification.InputHash) || !validSimulationHash(verification.FingerprintHash) || !validSimulationHash(verification.SourceResultHash) || !validSimulationHash(verification.ReproductionResultHash) || (verification.Status != "verified" && verification.Status != "mismatch") || verification.CreatedAt.IsZero() {
		return ErrSimulationRunInvalid
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO simulation_verifications(id,source_run_id,reproduction_run_id,input_hash,fingerprint_hash,source_result_hash,reproduction_result_hash,status,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, verification.ID, verification.SourceRunID, verification.ReproductionRunID, verification.InputHash, verification.FingerprintHash, verification.SourceResultHash, verification.ReproductionResultHash, verification.Status, verification.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func validSimulationHash(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}
