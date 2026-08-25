package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
)

const MaxAIRunStoredBytesV1 = 8 << 20

var ErrAIRunCapacity = errors.New("AI design run exceeds its durable payload capacity")

func ensureAIRunCapacity(ctx context.Context, tx *sql.Tx, jobID, projectID domain.ID, additionalBytes int) error {
	if additionalBytes < 0 || additionalBytes > MaxAIRunStoredBytesV1 {
		return ErrAIRunCapacity
	}
	var stored int64
	err := tx.QueryRowContext(ctx, `SELECT
		length(r.canonical_input) +
		COALESCE((SELECT sum(length(a.canonical_manifest)) FROM ai_attempts a WHERE a.job_id=r.job_id),0) +
		COALESCE((SELECT sum(length(x.stored_body)) FROM ai_attempt_responses x JOIN ai_attempts a ON a.attempt_id=x.attempt_id WHERE a.job_id=r.job_id),0) +
		COALESCE((SELECT sum(length(m.canonical_manifest)) FROM ai_evidence_manifests m WHERE m.job_id=r.job_id),0) +
		COALESCE((SELECT sum(length(e.canonical_evidence)) FROM ai_evidence_refs e WHERE e.job_id=r.job_id),0) +
		COALESCE((SELECT sum(length(p.canonical_patch)) FROM ai_draft_patches p WHERE p.job_id=r.job_id),0) +
		COALESCE((SELECT sum(length(e.canonical_event)) FROM ai_audit_events e JOIN ai_attempts a ON a.attempt_id=e.attempt_id WHERE a.job_id=r.job_id),0) +
		COALESCE((SELECT sum(length(d.canonical_event)) FROM ai_job_event_details d WHERE d.job_id=r.job_id),0) +
		COALESCE((SELECT sum(b.byte_size) FROM ai_blobs b WHERE b.content_hash IN (
			SELECT p.raw_redacted_blob_hash FROM ai_draft_patches p WHERE p.job_id=r.job_id AND p.raw_redacted_blob_hash IS NOT NULL
			UNION SELECT p.diff_blob_hash FROM ai_draft_patches p WHERE p.job_id=r.job_id AND p.diff_blob_hash IS NOT NULL
			UNION SELECT c.input_blob_hash FROM ai_tool_calls c JOIN ai_attempts a ON a.attempt_id=c.attempt_id WHERE a.job_id=r.job_id AND c.input_blob_hash IS NOT NULL
			UNION SELECT c.result_blob_hash FROM ai_tool_calls c JOIN ai_attempts a ON a.attempt_id=c.attempt_id WHERE a.job_id=r.job_id AND c.result_blob_hash IS NOT NULL
		)),0)
		FROM ai_design_runs r WHERE r.job_id=? AND r.project_uuid=?`, jobID, projectID).Scan(&stored)
	if err != nil {
		return err
	}
	if stored+int64(additionalBytes) > MaxAIRunStoredBytesV1 {
		return ErrAIRunCapacity
	}
	return nil
}
