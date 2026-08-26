package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var (
	ErrImpactInvalid  = errors.New("impact report request is invalid")
	ErrImpactNotFound = errors.New("impact report not found")
	ErrImpactConflict = errors.New("impact report identity conflict")
)

var _ impact.ReportStore = (*Store)(nil)
var _ impact.CompletionStore = (*Store)(nil)

func (s *Store) CreateOrGetStaging(ctx context.Context, jobID domain.ID, input impact.Input, inputHash string) (domain.ID, bool, error) {
	if !jobID.Valid() || input.ProjectID != s.projectID || !input.Base.Valid() || !input.Target.Valid() || !impact.ValidHash(inputHash) {
		return "", false, ErrImpactInvalid
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return "", false, err
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	var existingID domain.ID
	var existingJSON string
	err = tx.QueryRowContext(ctx, `SELECT id,input_json FROM impact_staging WHERE project_uuid=? AND input_hash=?`, s.projectID, inputHash).Scan(&existingID, &existingJSON)
	if err == nil {
		if existingJSON != string(inputJSON) {
			return "", false, ErrImpactConflict
		}
		return existingID, true, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	id, err := domain.NewID()
	if err != nil {
		return "", false, err
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, `INSERT INTO impact_staging(id,job_id,project_uuid,input_hash,input_json,phase,updated_at) VALUES(?,?,?,?,?,'admission',?)`, id, jobID, s.projectID, inputHash, string(inputJSON), now)
	if err != nil {
		return "", false, err
	}
	if err = tx.Commit(); err != nil {
		return "", false, err
	}
	return id, false, nil
}

func (s *Store) LoadStaging(ctx context.Context, jobID domain.ID) (domain.ID, impact.Input, string, string, bool, error) {
	if !jobID.Valid() {
		return "", impact.Input{}, "", "", false, ErrImpactInvalid
	}
	var id domain.ID
	var encoded, inputHash, phase string
	err := s.db.QueryRowContext(ctx, `SELECT id,input_json,input_hash,phase FROM impact_staging WHERE job_id=? AND project_uuid=?`, jobID, s.projectID).Scan(&id, &encoded, &inputHash, &phase)
	if errors.Is(err, sql.ErrNoRows) {
		return "", impact.Input{}, "", "", false, nil
	}
	if err != nil {
		return "", impact.Input{}, "", "", false, err
	}
	var input impact.Input
	if err = json.Unmarshal([]byte(encoded), &input); err != nil || !id.Valid() || !impact.ValidHash(inputHash) {
		return "", impact.Input{}, "", "", false, ErrImpactInvalid
	}
	return id, input, inputHash, phase, true, nil
}

func (s *Store) SaveChanged(ctx context.Context, stagingID domain.ID, changed []impact.ChangedEntity) error {
	return s.updateImpactStaging(ctx, stagingID, "diff", "changed_json", changed)
}

func (s *Store) SaveTraversal(ctx context.Context, stagingID domain.ID, affected []impact.AffectedEntity, reasons []impact.TruncationReason, warnings []string) error {
	affectedJSON, err := json.Marshal(affected)
	if err != nil {
		return err
	}
	reasonsJSON, _ := json.Marshal(reasons)
	warningsJSON, _ := json.Marshal(warnings)
	result, err := s.db.ExecContext(ctx, `UPDATE impact_staging SET phase='traversal',affected_json=?,truncation_json=?,warnings_json=?,updated_at=? WHERE id=? AND project_uuid=?`, string(affectedJSON), string(reasonsJSON), string(warningsJSON), s.now().UTC().Format(time.RFC3339Nano), stagingID, s.projectID)
	return requireOneImpactRow(result, err)
}

func (s *Store) SaveDefaultPaths(ctx context.Context, stagingID domain.ID, affected []impact.AffectedEntity) error {
	return s.updateImpactStaging(ctx, stagingID, "default_paths", "affected_json", affected)
}

func (s *Store) SaveSuspected(ctx context.Context, stagingID domain.ID, state impact.SuspectedState, evidence []impact.SuspectedEvidence, warnings []string) error {
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	warningsJSON, _ := json.Marshal(warnings)
	result, err := s.db.ExecContext(ctx, `UPDATE impact_staging SET phase='optional_retrieval',suspected_state=?,suspected_json=?,warnings_json=?,updated_at=? WHERE id=? AND project_uuid=?`, state, string(evidenceJSON), string(warningsJSON), s.now().UTC().Format(time.RFC3339Nano), stagingID, s.projectID)
	return requireOneImpactRow(result, err)
}

func (s *Store) updateImpactStaging(ctx context.Context, stagingID domain.ID, phase, column string, value any) error {
	if !stagingID.Valid() || (column != "changed_json" && column != "affected_json") {
		return ErrImpactInvalid
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	query := `UPDATE impact_staging SET phase=?,` + column + `=?,updated_at=? WHERE id=? AND project_uuid=?`
	result, err := s.db.ExecContext(ctx, query, phase, string(encoded), s.now().UTC().Format(time.RFC3339Nano), stagingID, s.projectID)
	return requireOneImpactRow(result, err)
}

func (s *Store) Seal(ctx context.Context, report impact.Report) (impact.Report, bool, error) {
	if !report.ID.Valid() || report.Input.ProjectID != s.projectID || !report.Input.Base.Valid() || !report.Input.Target.Valid() || !impact.ValidHash(report.InputHash) || !impact.ValidHash(report.ResultHash) || report.CreatedAt.IsZero() {
		return impact.Report{}, false, ErrImpactInvalid
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return impact.Report{}, false, err
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return impact.Report{}, false, err
	}
	defer tx.Rollback()
	if existing, found, findErr := findImpactReport(ctx, tx, s.projectID, "input_hash", report.InputHash); findErr != nil {
		return impact.Report{}, false, findErr
	} else if found {
		if existing.ResultHash != report.ResultHash {
			return impact.Report{}, false, ErrImpactConflict
		}
		return existing, true, tx.Commit()
	}
	if err = s.inject("impact-before-seal"); err != nil {
		return impact.Report{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO impact_reports(id,project_uuid,base_revision_id,target_revision_id,input_hash,result_hash,analysis_contract_version,base_graph_hash,target_graph_hash,report_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, report.ID, s.projectID, report.Input.Base.RevisionID, report.Input.Target.RevisionID, report.InputHash, report.ResultHash, report.Input.AnalysisContractVersion, report.Input.Base.GraphManifestHash, report.Input.Target.GraphManifestHash, string(encoded), report.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return impact.Report{}, false, err
	}
	if err = insertImpactChildren(ctx, tx, report); err != nil {
		return impact.Report{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM impact_staging WHERE project_uuid=? AND input_hash=?`, s.projectID, report.InputHash); err != nil {
		return impact.Report{}, false, err
	}
	if err = s.inject("impact-after-seal"); err != nil {
		return impact.Report{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return impact.Report{}, false, err
	}
	return report, false, nil
}

// SealImpactSuccess commits the immutable report, shared Job result and an
// exact matching Graph handoff in one SQLite transaction. A canceled Job can
// never be sealed because cancel_generation participates in the Job CAS.
func (s *Store) SealImpactSuccess(ctx context.Context, report impact.Report, jobID domain.ID, observedCancelGeneration int64) (impact.Report, bool, error) {
	if !report.ID.Valid() || !jobID.Valid() || observedCancelGeneration < 0 || report.Input.ProjectID != s.projectID || !impact.ValidHash(report.InputHash) || !impact.ValidHash(report.ResultHash) || report.CreatedAt.IsZero() {
		return impact.Report{}, false, ErrImpactInvalid
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return impact.Report{}, false, err
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return impact.Report{}, false, err
	}
	defer tx.Rollback()
	replay := false
	if existing, found, findErr := findImpactReport(ctx, tx, s.projectID, "input_hash", report.InputHash); findErr != nil {
		return impact.Report{}, false, findErr
	} else if found {
		if existing.ResultHash != report.ResultHash {
			return impact.Report{}, false, ErrImpactConflict
		}
		report, replay = existing, true
	} else {
		if err = s.inject("impact-before-seal"); err != nil {
			return impact.Report{}, false, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO impact_reports(id,project_uuid,base_revision_id,target_revision_id,input_hash,result_hash,analysis_contract_version,base_graph_hash,target_graph_hash,report_json,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, report.ID, s.projectID, report.Input.Base.RevisionID, report.Input.Target.RevisionID, report.InputHash, report.ResultHash, report.Input.AnalysisContractVersion, report.Input.Base.GraphManifestHash, report.Input.Target.GraphManifestHash, string(encoded), report.CreatedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return impact.Report{}, false, err
		}
		if err = insertImpactChildren(ctx, tx, report); err != nil {
			return impact.Report{}, false, err
		}
	}
	resultURL := "/api/v1/impact-analyses/" + string(report.ID)
	write, err := tx.ExecContext(ctx, `UPDATE jobs SET status='succeeded',result_type='impact_analysis',result_id=?,result_url=?,updated_at=? WHERE id=? AND project_uuid=? AND kind='impact_analysis' AND status IN ('running','interrupted') AND cancel_generation=?`, report.ID, resultURL, s.now().UTC().Format(time.RFC3339Nano), jobID, s.projectID, observedCancelGeneration)
	if err != nil {
		return impact.Report{}, false, err
	}
	if rows, rowsErr := write.RowsAffected(); rowsErr != nil || rows != 1 {
		return impact.Report{}, false, ErrImpactConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE graph_impact_handoffs SET status='consumed',job_id=?,report_id=?,safe_reason=NULL,updated_at=? WHERE revision_id=? AND graph_manifest_hash=? AND stage='impact' AND status IN ('queued','waiting','claimed')`, jobID, report.ID, s.now().UTC().Format(time.RFC3339Nano), report.Input.Target.RevisionID, report.Input.Target.GraphManifestHash)
	if err != nil {
		return impact.Report{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM impact_staging WHERE project_uuid=? AND input_hash=?`, s.projectID, report.InputHash); err != nil {
		return impact.Report{}, false, err
	}
	if err = s.inject("impact-after-seal"); err != nil {
		return impact.Report{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return impact.Report{}, false, err
	}
	return report, replay, nil
}

func insertImpactChildren(ctx context.Context, tx *sql.Tx, report impact.Report) error {
	for ordinal, changed := range report.Changed {
		encoded, _ := json.Marshal(changed)
		if _, err := tx.ExecContext(ctx, `INSERT INTO impact_changed_entities(report_id,ordinal,entity_id,entity_kind,change_kind,record_json) VALUES(?,?,?,?,?,?)`, report.ID, ordinal, changed.EntityID, changed.Kind, changed.ChangeKind, string(encoded)); err != nil {
			return err
		}
	}
	for ordinal, affected := range report.Affected {
		encoded, _ := json.Marshal(affected)
		if _, err := tx.ExecContext(ctx, `INSERT INTO impact_affected_entities(report_id,ordinal,node_id,minimum_depth,record_json) VALUES(?,?,?,?,?)`, report.ID, ordinal, affected.Node.ID, affected.MinimumDepth, string(encoded)); err != nil {
			return err
		}
		if affected.DefaultPath != nil {
			pathJSON, _ := json.Marshal(affected.DefaultPath)
			if _, err := tx.ExecContext(ctx, `INSERT INTO impact_paths(report_id,expansion_hash,target_node_id,path_ordinal,path_json) VALUES(?,'default',?,?,?)`, report.ID, affected.Node.ID, 0, string(pathJSON)); err != nil {
				return err
			}
		}
	}
	for _, evidence := range report.Suspected {
		encoded, _ := json.Marshal(evidence)
		if _, err := tx.ExecContext(ctx, `INSERT INTO impact_suspected_evidence(report_id,rank,node_id,evidence_json) VALUES(?,?,?,?)`, report.ID, evidence.Rank, evidence.Node.ID, string(encoded)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetImpactReport(ctx context.Context, reportID domain.ID) (impact.Report, error) {
	if !reportID.Valid() {
		return impact.Report{}, ErrImpactNotFound
	}
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT report_json FROM impact_reports WHERE id=? AND project_uuid=?`, reportID, s.projectID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return impact.Report{}, ErrImpactNotFound
	}
	if err != nil {
		return impact.Report{}, err
	}
	return decodeImpactReport(encoded)
}

// ImpactJobForReport returns the earliest durable impact Job linked to the
// immutable report. Cache-link Jobs may be newer, so ordering preserves the
// original provenance link for historical reads.
func (s *Store) ImpactJobForReport(ctx context.Context, reportID domain.ID) (sharedjob.Record, bool, error) {
	if !reportID.Valid() {
		return sharedjob.Record{}, false, ErrImpactNotFound
	}
	record, err := scanSharedJob(s.db.QueryRowContext(ctx, sharedJobSelect+` WHERE project_uuid=? AND kind='impact_analysis' AND result_id=? ORDER BY created_at,id LIMIT 1`, s.projectID, reportID))
	if errors.Is(err, sql.ErrNoRows) {
		return sharedjob.Record{}, false, nil
	}
	return record, err == nil, err
}

func (s *Store) FindByInputHash(ctx context.Context, inputHash string) (impact.Report, bool, error) {
	if !impact.ValidHash(inputHash) {
		return impact.Report{}, false, ErrImpactInvalid
	}
	return findImpactReport(ctx, s.db, s.projectID, "input_hash", inputHash)
}

type impactRowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func findImpactReport(ctx context.Context, query impactRowQuerier, projectID domain.ID, column, value string) (impact.Report, bool, error) {
	if column != "input_hash" {
		return impact.Report{}, false, ErrImpactInvalid
	}
	var encoded string
	err := query.QueryRowContext(ctx, `SELECT report_json FROM impact_reports WHERE project_uuid=? AND `+column+`=?`, projectID, value).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return impact.Report{}, false, nil
	}
	if err != nil {
		return impact.Report{}, false, err
	}
	report, err := decodeImpactReport(encoded)
	return report, err == nil, err
}

func decodeImpactReport(encoded string) (impact.Report, error) {
	var report impact.Report
	if err := json.Unmarshal([]byte(encoded), &report); err != nil || !report.ID.Valid() || !impact.ValidHash(report.InputHash) || !impact.ValidHash(report.ResultHash) {
		return impact.Report{}, ErrImpactInvalid
	}
	return report, nil
}

func (s *Store) ExpandPaths(ctx context.Context, reportID domain.ID, expansionHash, targetNodeID string, paths []impact.Path) (bool, error) {
	if !reportID.Valid() || !impact.ValidHash(expansionHash) || targetNodeID == "" {
		return false, ErrImpactInvalid
	}
	for _, path := range paths {
		if path.TargetNodeID != targetNodeID {
			return false, ErrImpactInvalid
		}
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO impact_path_expansions(report_id,expansion_hash,target_node_id,path_count,created_at) SELECT ?,?,?,?,? WHERE EXISTS(SELECT 1 FROM impact_reports WHERE id=? AND project_uuid=?)`, reportID, expansionHash, targetNodeID, len(paths), s.now().UTC().Format(time.RFC3339Nano), reportID, s.projectID)
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if inserted == 0 {
		if err = tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	for ordinal, path := range paths {
		encoded, _ := json.Marshal(path)
		result, insertErr := tx.ExecContext(ctx, `INSERT OR IGNORE INTO impact_paths(report_id,expansion_hash,target_node_id,path_ordinal,path_json) SELECT ?,?,?,?,? WHERE EXISTS(SELECT 1 FROM impact_reports WHERE id=? AND project_uuid=?)`, reportID, expansionHash, path.TargetNodeID, ordinal, string(encoded), reportID, s.projectID)
		if insertErr != nil {
			return false, insertErr
		}
		rows, _ := result.RowsAffected()
		if rows != 1 {
			return false, ErrImpactConflict
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

func (s *Store) SaveExplanation(ctx context.Context, reportID domain.ID, attempt impact.ExplanationAttempt) error {
	if !reportID.Valid() || attempt.ReportID != reportID || !attempt.ID.Valid() || !impact.ValidHash(attempt.InputHash) || attempt.Status == "" || attempt.CreatedAt.IsZero() {
		return ErrImpactInvalid
	}
	encoded, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO impact_explanations(id,report_id,input_hash,status,explanation_json,created_at) SELECT ?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM impact_reports WHERE id=? AND project_uuid=?) ON CONFLICT(report_id,input_hash) DO NOTHING`, attempt.ID, reportID, attempt.InputHash, attempt.Status, string(encoded), attempt.CreatedAt.UTC().Format(time.RFC3339Nano), reportID, s.projectID)
	return err
}

func (s *Store) FindExplanation(ctx context.Context, reportID domain.ID, inputHash string) (impact.ExplanationAttempt, bool, error) {
	if !reportID.Valid() || !impact.ValidHash(inputHash) {
		return impact.ExplanationAttempt{}, false, ErrImpactInvalid
	}
	var encoded string
	err := s.db.QueryRowContext(ctx, `SELECT explanation_json FROM impact_explanations e JOIN impact_reports r ON r.id=e.report_id WHERE e.report_id=? AND e.input_hash=? AND r.project_uuid=?`, reportID, inputHash, s.projectID).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return impact.ExplanationAttempt{}, false, nil
	}
	if err != nil {
		return impact.ExplanationAttempt{}, false, err
	}
	var attempt impact.ExplanationAttempt
	if err = json.Unmarshal([]byte(encoded), &attempt); err != nil || !attempt.ID.Valid() || attempt.ReportID != reportID || attempt.InputHash != inputHash {
		return impact.ExplanationAttempt{}, false, ErrImpactInvalid
	}
	return attempt, true, nil
}

func requireOneImpactRow(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrImpactNotFound
	}
	return nil
}
