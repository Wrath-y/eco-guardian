package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

type ValidationReport struct {
	Run    validation.ValidationRun
	Issues []validation.Issue
}

func (s *Store) InsertCompletedValidationRun(ctx context.Context, run validation.ValidationRun, issues []validation.Issue) error {
	if run.Status != validation.RunCompleted {
		return fmt.Errorf("only completed runs may be inserted")
	}
	for _, issue := range issues {
		if !issue.Valid() {
			return fmt.Errorf("invalid validation issue")
		}
	}
	manifestHash, err := run.Versions.Hash()
	if err != nil {
		return err
	}
	manifest, err := json.Marshal(run.Versions)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.insertCompletedValidationRunTx(ctx, tx, run, issues, manifestHash, manifest); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) insertCompletedValidationRunTx(ctx context.Context, tx *sql.Tx, run validation.ValidationRun, issues []validation.Issue, manifestHash string, manifest []byte) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO validation_runs(id,source_kind,source_revision_id,source_input_hash,scope,version_manifest_hash,version_manifest,status,error_count,block_count,warning_count,info_count,result_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, run.Source.Kind, nullID(run.Source.RevisionID), run.Source.InputHash, run.Scope, manifestHash, string(manifest), run.Status, run.Summary.Error, run.Summary.Block, run.Summary.Warning, run.Summary.Info, run.ResultHash, run.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	if err = s.inject("validation_run"); err != nil {
		return err
	}
	for ordinal, issue := range validation.SortIssues(issues) {
		params, _ := json.Marshal(issue.MessageParams)
		evidence, _ := json.Marshal(issue.Evidence)
		var start, end any
		if issue.Span != nil {
			start = issue.Span.StartByte
			end = issue.Span.EndByte
		}
		var issueOrdinal any
		if issue.Ordinal != nil {
			issueOrdinal = *issue.Ordinal
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO validation_issues(run_id,ordinal,fingerprint,severity,code,entity_id,field_path,span_start,span_end,issue_ordinal,message_key,message_params,fix_hint_key,evidence) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, run.ID, ordinal, issue.Fingerprint, issue.Severity, issue.Code, issue.EntityID, issue.FieldPath, start, end, issueOrdinal, issue.MessageKey, string(params), nullString(issue.FixHintKey), string(evidence))
		if err != nil {
			return err
		}
		if err = s.inject("validation_issue"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetValidationReport(ctx context.Context, id string) (ValidationReport, error) {
	var report ValidationReport
	var manifest, created string
	var revision sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,source_kind,source_revision_id,source_input_hash,scope,version_manifest,status,error_count,block_count,warning_count,info_count,result_hash,created_at FROM validation_runs WHERE id=?`, id).Scan(&report.Run.ID, &report.Run.Source.Kind, &revision, &report.Run.Source.InputHash, &report.Run.Scope, &manifest, &report.Run.Status, &report.Run.Summary.Error, &report.Run.Summary.Block, &report.Run.Summary.Warning, &report.Run.Summary.Info, &report.Run.ResultHash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return report, ErrNotFound
	}
	if err != nil {
		return report, err
	}
	if revision.Valid {
		report.Run.Source.RevisionID = domain.ID(revision.String)
	}
	if err = json.Unmarshal([]byte(manifest), &report.Run.Versions); err != nil {
		return report, err
	}
	report.Run.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return report, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT fingerprint,severity,code,entity_id,field_path,span_start,span_end,issue_ordinal,message_key,message_params,fix_hint_key,evidence FROM validation_issues WHERE run_id=? ORDER BY ordinal`, id)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	for rows.Next() {
		var issue validation.Issue
		var start, end, ordinal sql.NullInt64
		var params, evidence string
		var hint sql.NullString
		if err = rows.Scan(&issue.Fingerprint, &issue.Severity, &issue.Code, &issue.EntityID, &issue.FieldPath, &start, &end, &ordinal, &issue.MessageKey, &params, &hint, &evidence); err != nil {
			return report, err
		}
		if start.Valid {
			span := validation.FormulaSpan{StartByte: int(start.Int64), EndByte: int(end.Int64)}
			issue.Span = &span
		}
		if ordinal.Valid {
			value := int(ordinal.Int64)
			issue.Ordinal = &value
		}
		if hint.Valid {
			issue.FixHintKey = hint.String
		}
		_ = json.Unmarshal([]byte(params), &issue.MessageParams)
		_ = json.Unmarshal([]byte(evidence), &issue.Evidence)
		report.Issues = append(report.Issues, issue)
	}
	return report, rows.Err()
}

func (s *Store) FindMatchingFullRun(ctx context.Context, revisionID domain.ID, configHash string, versions validation.VersionManifest) (validation.ValidationRun, bool, error) {
	hash, err := versions.Hash()
	if err != nil {
		return validation.ValidationRun{}, false, err
	}
	var id string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM validation_runs WHERE source_kind='revision' AND source_revision_id=? AND source_input_hash=? AND scope='FULL' AND version_manifest_hash=? AND status='completed' ORDER BY created_at DESC,id DESC LIMIT 1`, revisionID, configHash, hash).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return validation.ValidationRun{}, false, nil
	}
	if err != nil {
		return validation.ValidationRun{}, false, err
	}
	report, err := s.GetValidationReport(ctx, id)
	return report.Run, err == nil, err
}

// FullValidationWarningCodes returns only the stable warning codes recorded
// with the exact FULL validation report. Graph orchestration must not persist
// paths, issue parameters, or raw validation evidence in its Job evidence.
func (s *Store) FullValidationWarningCodes(ctx context.Context, revisionID domain.ID, configHash string, versions validation.VersionManifest) ([]string, error) {
	run, found, err := s.FindMatchingFullRun(ctx, revisionID, configHash, versions)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT code FROM validation_issues WHERE run_id=? AND severity='WARNING' ORDER BY code`, run.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var codes []string
	for rows.Next() {
		var code string
		if err = rows.Scan(&code); err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, rows.Err()
}
func nullID(value domain.ID) any {
	if value == "" {
		return nil
	}
	return value
}
func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
