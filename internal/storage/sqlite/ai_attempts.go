package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var _ aiorchestration.AttemptLedgerRepository = (*Store)(nil)

func (s *Store) InsertAttempt(ctx context.Context, record aiorchestration.AttemptRecord) (aiorchestration.AttemptRecord, bool, error) {
	if ctx == nil || !record.Valid() {
		return aiorchestration.AttemptRecord{}, false, aiorchestration.ErrAttemptLedgerInvalid
	}
	manifest, err := domain.CanonicalJSON(record.Manifest)
	if err != nil || len(manifest) > 131072 {
		return aiorchestration.AttemptRecord{}, false, aiorchestration.ErrAttemptLedgerInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return aiorchestration.AttemptRecord{}, false, err
	}
	defer tx.Rollback()
	if existing, found, findErr := findAIAttempt(ctx, tx, record.AttemptID, s.projectID); findErr != nil {
		return aiorchestration.AttemptRecord{}, false, findErr
	} else if found {
		if !equalAIPersistence(existing, record) {
			return aiorchestration.AttemptRecord{}, false, aiorchestration.ErrAttemptLedgerConflict
		}
		return existing, true, tx.Commit()
	}
	if err = validateAIAttemptLineage(ctx, tx, record, s.projectID); err != nil {
		return aiorchestration.AttemptRecord{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_attempts(attempt_id,job_id,ordinal,attempt_kind,repair_round,parent_job_id,parent_attempt_id,canonical_manifest,manifest_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, record.AttemptID, record.JobID, record.Ordinal, record.Kind, record.RepairRound, nullableDomainID(record.ParentJobID), nullableAttemptID(record.ParentID), manifest, record.ManifestHash, formatAIJobTime(s.now().UTC()))
	if err != nil {
		return aiorchestration.AttemptRecord{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return aiorchestration.AttemptRecord{}, false, err
	}
	return record, false, nil
}

func (s *Store) InsertTerminalResponse(ctx context.Context, receipt aiorchestration.TerminalResponseReceipt) (aiorchestration.TerminalResponseReceipt, bool, error) {
	if ctx == nil || !receipt.Valid() {
		return aiorchestration.TerminalResponseReceipt{}, false, aiorchestration.ErrAttemptLedgerInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return aiorchestration.TerminalResponseReceipt{}, false, err
	}
	defer tx.Rollback()
	if existing, found, findErr := findAITerminalResponse(ctx, tx, receipt.AttemptID, s.projectID); findErr != nil {
		return aiorchestration.TerminalResponseReceipt{}, false, findErr
	} else if found {
		if !equalAIPersistence(existing, receipt) {
			return aiorchestration.TerminalResponseReceipt{}, false, aiorchestration.ErrAttemptLedgerConflict
		}
		return existing, true, tx.Commit()
	}
	if !aiAttemptBelongsToJob(ctx, tx, receipt.AttemptID, receipt.JobID, s.projectID) {
		return aiorchestration.TerminalResponseReceipt{}, false, aiorchestration.ErrAttemptLedgerConflict
	}
	response := receipt.Response
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_attempt_responses(attempt_id,schema_id,schema_version,schema_hash,original_body_hash,stored_body,stored_body_hash,receipt_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, receipt.AttemptID, response.Schema.ID, response.Schema.Version, response.Schema.Hash, response.OriginalBodyHash, response.StoredBody, response.StoredBodyHash, receipt.ReceiptHash, formatAIJobTime(s.now().UTC()))
	if err != nil {
		return aiorchestration.TerminalResponseReceipt{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return aiorchestration.TerminalResponseReceipt{}, false, err
	}
	return receipt, false, nil
}

func (s *Store) InsertAttemptOutcome(ctx context.Context, receipt aiorchestration.AttemptOutcomeReceipt) (aiorchestration.AttemptOutcomeReceipt, bool, error) {
	if ctx == nil || !receipt.Valid() {
		return aiorchestration.AttemptOutcomeReceipt{}, false, aiorchestration.ErrAttemptLedgerInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return aiorchestration.AttemptOutcomeReceipt{}, false, err
	}
	defer tx.Rollback()
	if existing, found, findErr := findAIOutcome(ctx, tx, receipt.AttemptID, s.projectID); findErr != nil {
		return aiorchestration.AttemptOutcomeReceipt{}, false, findErr
	} else if found {
		if existing != receipt {
			return aiorchestration.AttemptOutcomeReceipt{}, false, aiorchestration.ErrAttemptLedgerConflict
		}
		return existing, true, tx.Commit()
	}
	if !aiAttemptBelongsToJob(ctx, tx, receipt.AttemptID, receipt.JobID, s.projectID) {
		return aiorchestration.AttemptOutcomeReceipt{}, false, aiorchestration.ErrAttemptLedgerConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_attempt_outcomes(attempt_id,outcome,error_code,outcome_hash,created_at) VALUES(?,?,?,?,?)`, receipt.AttemptID, receipt.Outcome, nullableAIText(receipt.ErrorCode), receipt.OutcomeHash, formatAIJobTime(s.now().UTC()))
	if err != nil {
		return aiorchestration.AttemptOutcomeReceipt{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return aiorchestration.AttemptOutcomeReceipt{}, false, err
	}
	return receipt, false, nil
}

func (s *Store) InsertAttemptPatchSeal(ctx context.Context, seal aiorchestration.AttemptPatchSeal) (aiorchestration.AttemptPatchSeal, bool, error) {
	if ctx == nil || !seal.Valid() {
		return aiorchestration.AttemptPatchSeal{}, false, aiorchestration.ErrAttemptLedgerInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return aiorchestration.AttemptPatchSeal{}, false, err
	}
	defer tx.Rollback()
	if existing, found, findErr := findAIPatchSeal(ctx, tx, seal.AttemptID, s.projectID); findErr != nil {
		return aiorchestration.AttemptPatchSeal{}, false, findErr
	} else if found {
		if existing != seal {
			return aiorchestration.AttemptPatchSeal{}, false, aiorchestration.ErrAttemptLedgerConflict
		}
		return existing, true, tx.Commit()
	}
	if !aiAttemptBelongsToJob(ctx, tx, seal.AttemptID, seal.JobID, s.projectID) {
		return aiorchestration.AttemptPatchSeal{}, false, aiorchestration.ErrAttemptLedgerConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_attempt_patch_seals(attempt_id,patch_id,patch_hash,seal_hash,created_at) VALUES(?,?,?,?,?)`, seal.AttemptID, seal.PatchID, seal.PatchHash, seal.SealHash, formatAIJobTime(s.now().UTC()))
	if err != nil {
		return aiorchestration.AttemptPatchSeal{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return aiorchestration.AttemptPatchSeal{}, false, err
	}
	return seal, false, nil
}

func validateAIAttemptLineage(ctx context.Context, tx *sql.Tx, record aiorchestration.AttemptRecord, projectID domain.ID) error {
	var jobKind string
	if err := tx.QueryRowContext(ctx, `SELECT j.kind FROM jobs j JOIN ai_design_runs r ON r.job_id=j.id WHERE j.id=? AND j.project_uuid=?`, record.JobID, projectID).Scan(&jobKind); err != nil || jobKind != string(aiorchestration.AIJobKind) {
		return aiorchestration.ErrAttemptLedgerConflict
	}
	switch record.Kind {
	case aiorchestration.AttemptInitial:
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ai_attempts WHERE job_id=?`, record.JobID).Scan(&count); err != nil || count != 0 {
			return aiorchestration.ErrAttemptLedgerConflict
		}
	case aiorchestration.AttemptRepair:
		var parentJob domain.ID
		var parentOrdinal int
		if err := tx.QueryRowContext(ctx, `SELECT job_id,ordinal FROM ai_attempts WHERE attempt_id=?`, record.ParentID).Scan(&parentJob, &parentOrdinal); err != nil || parentJob != record.JobID || parentOrdinal+1 != record.Ordinal {
			return aiorchestration.ErrAttemptLedgerConflict
		}
	case aiorchestration.AttemptRetry:
		var parentJob domain.ID
		if err := tx.QueryRowContext(ctx, `SELECT a.job_id FROM ai_attempts a JOIN ai_design_runs r ON r.job_id=a.job_id WHERE a.attempt_id=? AND a.job_id=? AND r.project_uuid=?`, record.ParentID, record.ParentJobID, projectID).Scan(&parentJob); err != nil || parentJob != record.ParentJobID {
			return aiorchestration.ErrAttemptLedgerConflict
		}
	default:
		return aiorchestration.ErrAttemptLedgerInvalid
	}
	return nil
}

func findAIAttempt(ctx context.Context, tx *sql.Tx, attemptID aicontract.AttemptID, projectID domain.ID) (aiorchestration.AttemptRecord, bool, error) {
	var record aiorchestration.AttemptRecord
	var manifest []byte
	var parentJob, parentAttempt sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT a.job_id,a.attempt_id,a.attempt_kind,a.ordinal,a.repair_round,a.parent_job_id,a.parent_attempt_id,a.canonical_manifest,a.manifest_hash FROM ai_attempts a JOIN ai_design_runs r ON r.job_id=a.job_id WHERE a.attempt_id=? AND r.project_uuid=?`, attemptID, projectID).Scan(&record.JobID, &record.AttemptID, &record.Kind, &record.Ordinal, &record.RepairRound, &parentJob, &parentAttempt, &manifest, &record.ManifestHash)
	if errors.Is(err, sql.ErrNoRows) {
		return aiorchestration.AttemptRecord{}, false, nil
	}
	if err != nil {
		return aiorchestration.AttemptRecord{}, false, err
	}
	record.ParentJobID = domain.ID(parentJob.String)
	record.ParentID = aicontract.AttemptID(parentAttempt.String)
	if err = json.Unmarshal(manifest, &record.Manifest); err != nil || !record.Valid() {
		return aiorchestration.AttemptRecord{}, false, aiorchestration.ErrAttemptLedgerInvalid
	}
	return record, true, nil
}

func findAITerminalResponse(ctx context.Context, tx *sql.Tx, attemptID aicontract.AttemptID, projectID domain.ID) (aiorchestration.TerminalResponseReceipt, bool, error) {
	var receipt aiorchestration.TerminalResponseReceipt
	var response aiaudit.SealedProviderResponse
	err := tx.QueryRowContext(ctx, `SELECT a.job_id,r.attempt_id,r.schema_id,r.schema_version,r.schema_hash,r.original_body_hash,r.stored_body,r.stored_body_hash,r.receipt_hash FROM ai_attempt_responses r JOIN ai_attempts a ON a.attempt_id=r.attempt_id JOIN ai_design_runs d ON d.job_id=a.job_id WHERE r.attempt_id=? AND d.project_uuid=?`, attemptID, projectID).Scan(&receipt.JobID, &receipt.AttemptID, &response.Schema.ID, &response.Schema.Version, &response.Schema.Hash, &response.OriginalBodyHash, &response.StoredBody, &response.StoredBodyHash, &receipt.ReceiptHash)
	if errors.Is(err, sql.ErrNoRows) {
		return aiorchestration.TerminalResponseReceipt{}, false, nil
	}
	if err != nil {
		return aiorchestration.TerminalResponseReceipt{}, false, err
	}
	receipt.Response = response
	if !receipt.Valid() {
		return aiorchestration.TerminalResponseReceipt{}, false, aiorchestration.ErrAttemptLedgerInvalid
	}
	return receipt, true, nil
}

func findAIOutcome(ctx context.Context, tx *sql.Tx, attemptID aicontract.AttemptID, projectID domain.ID) (aiorchestration.AttemptOutcomeReceipt, bool, error) {
	var receipt aiorchestration.AttemptOutcomeReceipt
	var errorCode sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT a.job_id,o.attempt_id,o.outcome,o.error_code,o.outcome_hash FROM ai_attempt_outcomes o JOIN ai_attempts a ON a.attempt_id=o.attempt_id JOIN ai_design_runs d ON d.job_id=a.job_id WHERE o.attempt_id=? AND d.project_uuid=?`, attemptID, projectID).Scan(&receipt.JobID, &receipt.AttemptID, &receipt.Outcome, &errorCode, &receipt.OutcomeHash)
	if errors.Is(err, sql.ErrNoRows) {
		return aiorchestration.AttemptOutcomeReceipt{}, false, nil
	}
	if err != nil {
		return aiorchestration.AttemptOutcomeReceipt{}, false, err
	}
	receipt.ErrorCode = errorCode.String
	if !receipt.Valid() {
		return aiorchestration.AttemptOutcomeReceipt{}, false, aiorchestration.ErrAttemptLedgerInvalid
	}
	return receipt, true, nil
}

func findAIPatchSeal(ctx context.Context, tx *sql.Tx, attemptID aicontract.AttemptID, projectID domain.ID) (aiorchestration.AttemptPatchSeal, bool, error) {
	var seal aiorchestration.AttemptPatchSeal
	err := tx.QueryRowContext(ctx, `SELECT a.job_id,s.attempt_id,s.patch_id,s.patch_hash,s.seal_hash FROM ai_attempt_patch_seals s JOIN ai_attempts a ON a.attempt_id=s.attempt_id JOIN ai_design_runs d ON d.job_id=a.job_id WHERE s.attempt_id=? AND d.project_uuid=?`, attemptID, projectID).Scan(&seal.JobID, &seal.AttemptID, &seal.PatchID, &seal.PatchHash, &seal.SealHash)
	if errors.Is(err, sql.ErrNoRows) {
		return aiorchestration.AttemptPatchSeal{}, false, nil
	}
	if err != nil {
		return aiorchestration.AttemptPatchSeal{}, false, err
	}
	if !seal.Valid() {
		return aiorchestration.AttemptPatchSeal{}, false, aiorchestration.ErrAttemptLedgerInvalid
	}
	return seal, true, nil
}

func aiAttemptBelongsToJob(ctx context.Context, tx *sql.Tx, attemptID aicontract.AttemptID, jobID, projectID domain.ID) bool {
	var count int
	return tx.QueryRowContext(ctx, `SELECT count(*) FROM ai_attempts a JOIN ai_design_runs r ON r.job_id=a.job_id WHERE a.attempt_id=? AND a.job_id=? AND r.project_uuid=?`, attemptID, jobID, projectID).Scan(&count) == nil && count == 1
}

func equalAIPersistence(left, right any) bool {
	leftJSON, leftErr := domain.CanonicalJSON(left)
	rightJSON, rightErr := domain.CanonicalJSON(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func nullableDomainID(value domain.ID) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableAttemptID(value aicontract.AttemptID) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableAIText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
