package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	"github.com/zouyi/eco-guardian/internal/risk/orchestration"
)

var (
	ErrRiskReportInvalid = errors.New("risk report is invalid")
	ErrRiskReportSeal    = errors.New("risk report cannot be sealed")
	ErrRiskReportReplay  = errors.New("risk report replay mismatch")
)

var _ orchestration.ReportRepository = (*Store)(nil)
var _ orchestration.FailureStore = (*Store)(nil)

const riskReportSelect = `SELECT id,job_id,project_uuid,report_kind,source_report_id,input_hash,calculation_hash,evidence_hash,report_hash,candidate_revision_id,candidate_config_hash,candidate_manifest_hash,baseline_kind,baseline_release_id,baseline_revision_id,baseline_config_hash,baseline_manifest_hash,policy_id,policy_version,policy_hash,threshold_id,threshold_version,threshold_hash,validation_run_id,validation_version,validation_hash,implementation_refs,simulation_run_refs,canonical_report,decision_item_ids,decision_reason,created_at FROM risk_reviews`

func (s *Store) FailRiskJob(ctx context.Context, jobID domain.ID, cancelGeneration int64, code, detail string) (sharedjob.Record, bool, error) {
	if !jobID.Valid() || cancelGeneration < 0 || !stableRiskFailureCode(code) || strings.TrimSpace(detail) == "" || len(code)+len(detail)+2 > 1024 {
		return sharedjob.Record{}, false, ErrRiskReportInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	defer tx.Rollback()
	job, err := scanSharedJob(tx.QueryRowContext(ctx, sharedJobSelect+` WHERE id=? AND project_uuid=?`, jobID, s.projectID))
	if err != nil || string(job.Kind) != orchestration.RiskReviewJobKind || job.CancelGeneration != cancelGeneration || job.CancelGeneration != 0 {
		return sharedjob.Record{}, false, ErrRiskReportSeal
	}
	if job.Status == sharedjob.Failed {
		var stored sql.NullString
		if err = tx.QueryRowContext(ctx, `SELECT error FROM job_events WHERE job_id=? ORDER BY event_ordinal DESC LIMIT 1`, jobID).Scan(&stored); err != nil || !stored.Valid || stored.String != code+": "+detail {
			return sharedjob.Record{}, false, ErrRiskReportReplay
		}
		return job, true, tx.Commit()
	}
	if job.Status != sharedjob.Queued && job.Status != sharedjob.Running && job.Status != sharedjob.Interrupted {
		return sharedjob.Record{}, false, ErrRiskReportSeal
	}
	if err = s.inject("risk-fail-before-write"); err != nil {
		return sharedjob.Record{}, false, err
	}
	now := s.now().UTC()
	write, err := tx.ExecContext(ctx, `UPDATE jobs SET status='failed',updated_at=? WHERE id=? AND project_uuid=? AND kind=? AND status=? AND cancel_generation=0`, now.Format(time.RFC3339Nano), jobID, s.projectID, orchestration.RiskReviewJobKind, job.Status)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	if rows, _ := write.RowsAffected(); rows != 1 {
		return sharedjob.Record{}, false, ErrRiskReportSeal
	}
	var ordinal int64
	var progress int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(event_ordinal),0),COALESCE(MAX(progress),0) FROM job_events WHERE job_id=?`, jobID).Scan(&ordinal, &progress); err != nil {
		return sharedjob.Record{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO job_events(job_id,event_ordinal,phase,progress,error,created_at) VALUES(?,?,?,?,?,?)`, jobID, ordinal+1, "FAILED", progress, code+": "+detail, now.Format(time.RFC3339Nano)); err != nil {
		return sharedjob.Record{}, false, err
	}
	if err = s.inject("risk-fail-after-write"); err != nil {
		return sharedjob.Record{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return sharedjob.Record{}, false, err
	}
	updated, err := s.GetJob(ctx, jobID)
	return updated, false, err
}

func stableRiskFailureCode(code string) bool {
	switch code {
	case "RECOVERY_MISMATCH", "RECOVERY_UNAVAILABLE", "RULE_CONTRACT_INVALID", "RISK_CALCULATION_FAILED", "REPORT_CONTRACT_INVALID", "STORAGE_FAILURE":
		return true
	default:
		return false
	}
}

// SealRiskReport atomically publishes the immutable report and all ordered
// items together with the shared Job terminal result. No report row is visible
// if any identity check, item insert, cancellation check, or Job transition
// fails.
func (s *Store) SealRiskReport(ctx context.Context, report orchestration.Report) error {
	if !report.Valid() || report.ProjectID != s.projectID {
		return ErrRiskReportInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	job, err := scanSharedJob(tx.QueryRowContext(ctx, sharedJobSelect+` WHERE id=? AND project_uuid=?`, report.JobID, s.projectID))
	if err != nil || string(job.Kind) != orchestration.RiskReviewJobKind || (job.Status != sharedjob.Running && job.Status != sharedjob.Interrupted) || job.RevisionID != report.Candidate.RevisionID || job.InputHash != report.InputHash || job.CancelGeneration != report.CancelGeneration {
		return ErrRiskReportSeal
	}
	if err = validateRiskReportReferences(ctx, tx, report); err != nil {
		return err
	}
	if report.Kind == riskcontract.DecisionReport {
		if err = validateDecisionSource(ctx, tx, report); err != nil {
			return err
		}
	}
	if err = s.inject("risk-seal-before-review"); err != nil {
		return err
	}
	if err = insertRiskReport(ctx, tx, report); err != nil {
		return err
	}
	if err = s.inject("risk-seal-after-review"); err != nil {
		return err
	}
	for _, item := range report.Items {
		if err = s.inject("risk-seal-before-item"); err != nil {
			return err
		}
		canonical, encodeErr := domain.CanonicalJSON(item.Item)
		if encodeErr != nil {
			return ErrRiskReportInvalid
		}
		var severity any
		if item.Item.Severity != nil {
			severity = string(*item.Item.Severity)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO risk_items(report_id,ordinal,item_id,policy_role,comparison_status,severity,rule_id,rule_version,rule_hash,override_classification,evidence_hash,item_hash,canonical_item) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, report.ID, item.Item.Ordinal, item.Item.ID, item.Item.Role, item.Item.Status, severity, item.Item.Rule.ID, item.Item.Rule.Version, item.Item.Rule.Hash, item.Item.Override, item.Item.EvidenceHash, item.ItemHash, string(canonical))
		if err != nil {
			return err
		}
		if err = s.inject("risk-seal-after-item"); err != nil {
			return err
		}
	}
	if err = s.inject("risk-seal-before-job-success"); err != nil {
		return err
	}
	write, err := tx.ExecContext(ctx, `UPDATE jobs SET status='succeeded',result_type='risk_review',result_id=?,result_url=?,updated_at=? WHERE id=? AND project_uuid=? AND kind=? AND status=? AND input_hash=? AND cancel_generation=?`, report.ID, "/api/v1/risk-reviews/"+string(report.ID), s.now().UTC().Format(time.RFC3339Nano), report.JobID, s.projectID, orchestration.RiskReviewJobKind, job.Status, report.InputHash, report.CancelGeneration)
	if err != nil {
		return err
	}
	if rows, _ := write.RowsAffected(); rows != 1 {
		return ErrRiskReportSeal
	}
	if err = s.inject("risk-seal-after-job-success"); err != nil {
		return err
	}
	return tx.Commit()
}

func insertRiskReport(ctx context.Context, tx *sql.Tx, report orchestration.Report) error {
	implementations := append([]riskcontract.Identity(nil), report.Implementations...)
	sort.Slice(implementations, func(i, j int) bool { return implementations[i].ID < implementations[j].ID })
	runs := append([]orchestration.SimulationRef(nil), report.SimulationRuns...)
	sort.Slice(runs, func(i, j int) bool { return runs[i].RunID < runs[j].RunID })
	implementationJSON, err := json.Marshal(implementations)
	if err != nil {
		return err
	}
	runJSON, err := json.Marshal(runs)
	if err != nil {
		return err
	}
	envelopeJSON, err := report.Envelope.CanonicalJSON()
	if err != nil {
		return err
	}
	var decisionItems, decisionReason any
	if report.Kind == riskcontract.DecisionReport {
		decisionItems, err = json.Marshal(report.Envelope.DecisionItemIDs)
		if err != nil {
			return err
		}
		decisionReason = report.Envelope.DecisionReason
	}
	var baselineReleaseID, baselineRevisionID, baselineConfigHash, baselineManifestHash any
	if report.Baseline.Revision != nil {
		baselineReleaseID = report.Baseline.ReleaseID
		baselineRevisionID = report.Baseline.Revision.RevisionID
		baselineConfigHash = report.Baseline.Revision.ConfigHash
		baselineManifestHash = report.Baseline.Revision.ManifestHash
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO risk_reviews(id,job_id,project_uuid,report_kind,source_report_id,input_hash,calculation_hash,evidence_hash,report_hash,candidate_revision_id,candidate_config_hash,candidate_manifest_hash,baseline_kind,baseline_release_id,baseline_revision_id,baseline_config_hash,baseline_manifest_hash,policy_id,policy_version,policy_hash,threshold_id,threshold_version,threshold_hash,validation_run_id,validation_version,validation_hash,implementation_refs,simulation_run_refs,canonical_report,decision_item_ids,decision_reason,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'SEALED',?)`, report.ID, report.JobID, report.ProjectID, report.Kind, nullID(report.SourceReportID), report.InputHash, report.CalculationHash, report.EvidenceHash, report.ReportHash, report.Candidate.RevisionID, report.Candidate.ConfigHash, report.Candidate.ManifestHash, report.Baseline.Kind, baselineReleaseID, baselineRevisionID, baselineConfigHash, baselineManifestHash, report.Policy.ID, report.Policy.Version, report.Policy.Hash, report.Threshold.ID, report.Threshold.Version, report.Threshold.Hash, report.Validation.ID, report.Validation.Version, report.Validation.Hash, string(implementationJSON), string(runJSON), string(envelopeJSON), decisionItems, decisionReason, report.CreatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func validateRiskReportReferences(ctx context.Context, tx *sql.Tx, report orchestration.Report) error {
	if err := validateRevisionRef(ctx, tx, report.Candidate); err != nil {
		return ErrRiskReportSeal
	}
	if report.Baseline.Revision != nil {
		var revisionID domain.ID
		if err := tx.QueryRowContext(ctx, `SELECT revision_id FROM releases WHERE id=?`, report.Baseline.ReleaseID).Scan(&revisionID); err != nil || revisionID != report.Baseline.Revision.RevisionID {
			return ErrRiskReportSeal
		}
		if err := validateRevisionRef(ctx, tx, *report.Baseline.Revision); err != nil {
			return ErrRiskReportSeal
		}
	}
	var policyVersion int64
	var policyHash string
	if err := tx.QueryRowContext(ctx, `SELECT display_version,canonical_hash FROM release_policies WHERE id=?`, report.Policy.ID).Scan(&policyVersion, &policyHash); err != nil || strconv.FormatInt(policyVersion, 10) != report.Policy.Version || policyHash != report.Policy.Hash {
		return ErrRiskReportSeal
	}
	var thresholdVersion int64
	var thresholdHash string
	var enabled int
	if err := tx.QueryRowContext(ctx, `SELECT display_version,body_hash,enabled FROM threshold_versions WHERE id=? AND project_uuid=?`, report.Threshold.ID, report.ProjectID).Scan(&thresholdVersion, &thresholdHash, &enabled); err != nil || strconv.FormatInt(thresholdVersion, 10) != report.Threshold.Version || thresholdHash != report.Threshold.Hash || enabled != 1 {
		return ErrRiskReportSeal
	}
	var validationRevision domain.ID
	var validationScope, validationHash string
	if err := tx.QueryRowContext(ctx, `SELECT source_revision_id,scope,result_hash FROM validation_runs WHERE id=?`, report.Validation.ID).Scan(&validationRevision, &validationScope, &validationHash); err != nil || validationRevision != report.Candidate.RevisionID || validationScope != "FULL" || validationHash != report.Validation.Hash {
		return ErrRiskReportSeal
	}
	for _, run := range report.SimulationRuns {
		var inputHash, fingerprintHash, resultHash string
		if err := tx.QueryRowContext(ctx, `SELECT input_hash,fingerprint_hash,result_hash FROM simulation_runs WHERE id=? AND project_uuid=?`, run.RunID, report.ProjectID).Scan(&inputHash, &fingerprintHash, &resultHash); err != nil || inputHash != run.InputHash || fingerprintHash != run.FingerprintHash || resultHash != run.ResultHash {
			return ErrRiskReportSeal
		}
	}
	return nil
}

func validateRevisionRef(ctx context.Context, tx *sql.Tx, ref orchestration.RevisionRef) error {
	var configHash, manifestHash string
	err := tx.QueryRowContext(ctx, `SELECT r.config_hash,m.version_manifest_hash FROM config_revisions r JOIN revision_metadata m ON m.revision_id=r.id WHERE r.id=?`, ref.RevisionID).Scan(&configHash, &manifestHash)
	if err != nil || configHash != ref.ConfigHash || manifestHash != ref.ManifestHash {
		return ErrRiskReportSeal
	}
	return nil
}

func validateDecisionSource(ctx context.Context, tx *sql.Tx, report orchestration.Report) error {
	var kind, calculationHash, evidenceHash string
	var candidateRevisionID domain.ID
	var policyID, thresholdID string
	if err := tx.QueryRowContext(ctx, `SELECT report_kind,calculation_hash,evidence_hash,candidate_revision_id,policy_id,threshold_id FROM risk_reviews WHERE id=? AND project_uuid=?`, report.SourceReportID, report.ProjectID).Scan(&kind, &calculationHash, &evidenceHash, &candidateRevisionID, &policyID, &thresholdID); err != nil {
		return ErrRiskReportSeal
	}
	if kind != string(riskcontract.CalculationReport) || calculationHash != report.CalculationHash || evidenceHash != report.EvidenceHash || candidateRevisionID != report.Candidate.RevisionID || policyID != report.Policy.ID || thresholdID != report.Threshold.ID {
		return ErrRiskReportSeal
	}
	for _, itemID := range report.Envelope.DecisionItemIDs {
		var severity, override string
		if err := tx.QueryRowContext(ctx, `SELECT severity,override_classification FROM risk_items WHERE report_id=? AND item_id=?`, report.SourceReportID, itemID).Scan(&severity, &override); err != nil || severity != string(riskcontract.Block) || override != string(riskcontract.NumericOverrideEligible) {
			return ErrRiskReportSeal
		}
	}
	return nil
}

func (s *Store) GetRiskReport(ctx context.Context, id domain.ID) (orchestration.Report, error) {
	if !id.Valid() {
		return orchestration.Report{}, ErrRiskReportInvalid
	}
	report, err := scanRiskReport(s.db.QueryRowContext(ctx, riskReportSelect+` WHERE id=? AND project_uuid=? AND status='SEALED'`, id, s.projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return orchestration.Report{}, ErrNotFound
	}
	if err != nil {
		return orchestration.Report{}, err
	}
	if report.Items, err = s.listRiskItems(ctx, id); err != nil {
		return orchestration.Report{}, err
	}
	if !report.Valid() {
		return orchestration.Report{}, ErrRiskReportInvalid
	}
	return report, nil
}

func (s *Store) ListRiskReports(ctx context.Context, query orchestration.HistoryQuery) ([]orchestration.Report, error) {
	if query.Limit == 0 {
		query.Limit = 50
	}
	if query.Limit < 1 || query.Limit > 100 || (query.CandidateRevisionID != "" && !query.CandidateRevisionID.Valid()) {
		return nil, ErrRiskReportInvalid
	}
	before := "9999-12-31T23:59:59.999999999Z"
	if !query.Before.IsZero() {
		before = query.Before.UTC().Format(time.RFC3339Nano)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM risk_reviews WHERE project_uuid=? AND status='SEALED' AND (?='' OR candidate_revision_id=?) AND created_at<? ORDER BY created_at DESC,id DESC LIMIT ?`, s.projectID, query.CandidateRevisionID, query.CandidateRevisionID, before, query.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	reports := make([]orchestration.Report, 0, len(ids))
	for _, id := range ids {
		report, getErr := s.GetRiskReport(ctx, id)
		if getErr != nil {
			return nil, getErr
		}
		reports = append(reports, report)
	}
	return reports, nil
}

type riskReportScanner interface{ Scan(...any) error }

func scanRiskReport(scanner riskReportScanner) (orchestration.Report, error) {
	var report orchestration.Report
	var kind, baselineKind, implementationsRaw, runsRaw, envelopeRaw, createdAt string
	var sourceReportID, baselineReleaseID, baselineRevisionID, baselineConfigHash, baselineManifestHash, decisionItems, decisionReason sql.NullString
	if err := scanner.Scan(&report.ID, &report.JobID, &report.ProjectID, &kind, &sourceReportID, &report.InputHash, &report.CalculationHash, &report.EvidenceHash, &report.ReportHash, &report.Candidate.RevisionID, &report.Candidate.ConfigHash, &report.Candidate.ManifestHash, &baselineKind, &baselineReleaseID, &baselineRevisionID, &baselineConfigHash, &baselineManifestHash, &report.Policy.ID, &report.Policy.Version, &report.Policy.Hash, &report.Threshold.ID, &report.Threshold.Version, &report.Threshold.Hash, &report.Validation.ID, &report.Validation.Version, &report.Validation.Hash, &implementationsRaw, &runsRaw, &envelopeRaw, &decisionItems, &decisionReason, &createdAt); err != nil {
		return orchestration.Report{}, err
	}
	report.Kind = riskcontract.ReportKind(kind)
	report.SourceReportID = domain.ID(sourceReportID.String)
	report.Baseline.Kind = riskcontract.BaselineKind(baselineKind)
	if baselineRevisionID.Valid {
		report.Baseline.ReleaseID = domain.ID(baselineReleaseID.String)
		report.Baseline.Revision = &orchestration.RevisionRef{RevisionID: domain.ID(baselineRevisionID.String), ConfigHash: baselineConfigHash.String, ManifestHash: baselineManifestHash.String}
	}
	if err := strictRiskJSON([]byte(implementationsRaw), &report.Implementations); err != nil {
		return orchestration.Report{}, err
	}
	if err := strictRiskJSON([]byte(runsRaw), &report.SimulationRuns); err != nil {
		return orchestration.Report{}, err
	}
	if err := strictRiskJSON([]byte(envelopeRaw), &report.Envelope); err != nil {
		return orchestration.Report{}, err
	}
	if report.Kind == riskcontract.DecisionReport {
		var storedItemIDs []string
		if !decisionItems.Valid || !decisionReason.Valid || strictRiskJSON([]byte(decisionItems.String), &storedItemIDs) != nil {
			return orchestration.Report{}, ErrRiskReportInvalid
		}
		sort.Strings(storedItemIDs)
		envelopeItemIDs := append([]string(nil), report.Envelope.DecisionItemIDs...)
		sort.Strings(envelopeItemIDs)
		if strings.Join(storedItemIDs, "\x00") != strings.Join(envelopeItemIDs, "\x00") || decisionReason.String != report.Envelope.DecisionReason {
			return orchestration.Report{}, ErrRiskReportInvalid
		}
	} else if decisionItems.Valid || decisionReason.Valid {
		return orchestration.Report{}, ErrRiskReportInvalid
	}
	parsedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return orchestration.Report{}, err
	}
	report.CreatedAt = parsedAt
	return report, nil
}

func (s *Store) listRiskItems(ctx context.Context, reportID domain.ID) ([]orchestration.StoredItem, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ordinal,item_id,policy_role,comparison_status,severity,rule_id,rule_version,rule_hash,override_classification,evidence_hash,item_hash,canonical_item FROM risk_items WHERE report_id=? ORDER BY ordinal`, reportID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []orchestration.StoredItem{}
	for rows.Next() {
		var stored orchestration.StoredItem
		var role, status, ruleID, ruleVersion, ruleHash, override, evidenceHash, canonical string
		var severity sql.NullString
		if err = rows.Scan(&stored.Item.Ordinal, &stored.Item.ID, &role, &status, &severity, &ruleID, &ruleVersion, &ruleHash, &override, &evidenceHash, &stored.ItemHash, &canonical); err != nil {
			return nil, err
		}
		if err = strictRiskJSON([]byte(canonical), &stored.Item); err != nil {
			return nil, err
		}
		if stored.Item.Ordinal != len(items) || stored.Item.Role != riskcontract.PolicyRole(role) || stored.Item.Status != riskcontract.ComparisonStatus(status) || stored.Item.Rule != (riskcontract.Identity{ID: ruleID, Version: ruleVersion, Hash: ruleHash}) || stored.Item.Override != riskcontract.OverrideClass(override) || stored.Item.EvidenceHash != evidenceHash || (severity.Valid && (stored.Item.Severity == nil || string(*stored.Item.Severity) != severity.String)) || (!severity.Valid && stored.Item.Severity != nil) || !stored.Valid() {
			return nil, ErrRiskReportInvalid
		}
		items = append(items, stored)
	}
	return items, rows.Err()
}

func strictRiskJSON(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("risk JSON has trailing content")
	}
	return nil
}

// ReconcileSealedRiskReport repairs a legacy/narrow crash window by linking an
// already sealed, hash-matched report to its original nonterminal Job. It
// never creates a second report.
func (s *Store) ReconcileSealedRiskReport(ctx context.Context, jobID domain.ID, inputHash, calculationHash string) (sharedjob.Record, bool, error) {
	if !jobID.Valid() || !validSimulationHash(inputHash) || !validSimulationHash(calculationHash) {
		return sharedjob.Record{}, false, ErrRiskReportReplay
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	defer tx.Rollback()
	var reportID domain.ID
	if err = tx.QueryRowContext(ctx, `SELECT id FROM risk_reviews WHERE job_id=? AND project_uuid=? AND input_hash=? AND calculation_hash=?`, jobID, s.projectID, inputHash, calculationHash).Scan(&reportID); err != nil {
		return sharedjob.Record{}, false, ErrRiskReportReplay
	}
	job, err := scanSharedJob(tx.QueryRowContext(ctx, sharedJobSelect+` WHERE id=? AND project_uuid=?`, jobID, s.projectID))
	if err != nil || string(job.Kind) != orchestration.RiskReviewJobKind || job.InputHash != inputHash || job.CancelGeneration != 0 {
		return sharedjob.Record{}, false, ErrRiskReportReplay
	}
	if job.Status == sharedjob.Succeeded {
		if job.Result == nil || job.Result.Type != "risk_review" || job.Result.ID != reportID {
			return sharedjob.Record{}, false, ErrRiskReportReplay
		}
		return job, true, tx.Commit()
	}
	if job.Status != sharedjob.Queued && job.Status != sharedjob.Running && job.Status != sharedjob.Interrupted {
		return sharedjob.Record{}, false, ErrRiskReportReplay
	}
	write, err := tx.ExecContext(ctx, `UPDATE jobs SET status='succeeded',result_type='risk_review',result_id=?,result_url=?,updated_at=? WHERE id=? AND project_uuid=? AND status=? AND cancel_generation=0`, reportID, "/api/v1/risk-reviews/"+string(reportID), s.now().UTC().Format(time.RFC3339Nano), jobID, s.projectID, job.Status)
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	if rows, _ := write.RowsAffected(); rows != 1 {
		return sharedjob.Record{}, false, ErrRiskReportReplay
	}
	if err = tx.Commit(); err != nil {
		return sharedjob.Record{}, false, err
	}
	updated, err := s.GetJob(ctx, jobID)
	return updated, false, err
}
