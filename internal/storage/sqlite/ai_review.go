package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	aiapplication "github.com/zouyi/eco-guardian/internal/ai/application"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var _ aiapplication.ReviewRepository = (*Store)(nil)

func (s *Store) ReadDraftPatchReview(ctx context.Context, patchID aicontract.PatchID) (aiapplication.DraftPatchReviewResource, error) {
	if ctx == nil || !patchID.Valid() || !domain.ID(patchID).Valid() {
		return aiapplication.DraftPatchReviewResource{}, aiapplication.ErrReviewInvalid
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return aiapplication.DraftPatchReviewResource{}, err
	}
	defer tx.Rollback()
	var resource aiapplication.DraftPatchReviewResource
	var canonicalPatch, canonicalInput, canonicalPreview, previewIssues []byte
	var storedPatchHash, storedPreviewHash aicontract.Hash
	var createdAt string
	err = tx.QueryRowContext(ctx, `SELECT p.job_id,p.canonical_patch,p.patch_hash,r.canonical_input,p.input_hash,p.canonical_preview,p.preview_hash,p.preview_issues,p.created_at
		FROM ai_draft_patches p JOIN ai_design_runs r ON r.job_id=p.job_id
		WHERE p.id=? AND r.project_uuid=?`, patchID, s.projectID).
		Scan(&resource.JobID, &canonicalPatch, &storedPatchHash, &canonicalInput, &resource.InputHash, &canonicalPreview, &storedPreviewHash, &previewIssues, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return aiapplication.DraftPatchReviewResource{}, aiapplication.ErrReviewNotFound
	}
	if err != nil {
		return aiapplication.DraftPatchReviewResource{}, err
	}
	if json.Unmarshal(canonicalPatch, &resource.Patch) != nil || json.Unmarshal(canonicalInput, &resource.Input) != nil || json.Unmarshal(previewIssues, &resource.PreviewIssues) != nil || resource.PreviewIssues == nil {
		return aiapplication.DraftPatchReviewResource{}, aiapplication.ErrReviewInvalid
	}
	patchCanonical, err := aicontract.CanonicalDraftPatch(resource.Patch)
	if err != nil || !bytes.Equal(patchCanonical, canonicalPatch) || resource.Patch.ID != patchID || resource.Patch.Hash != storedPatchHash {
		return aiapplication.DraftPatchReviewResource{}, aiapplication.ErrReviewInvalid
	}
	if len(canonicalPreview) != 0 {
		var preview aicontract.Preview
		if json.Unmarshal(canonicalPreview, &preview) != nil {
			return aiapplication.DraftPatchReviewResource{}, aiapplication.ErrReviewInvalid
		}
		canonical, canonicalErr := aicontract.CanonicalPreview(preview)
		hash, hashErr := aicontract.HashPreview(preview)
		if canonicalErr != nil || hashErr != nil || !bytes.Equal(canonical, canonicalPreview) || hash != storedPreviewHash {
			return aiapplication.DraftPatchReviewResource{}, aiapplication.ErrReviewInvalid
		}
		resource.Preview = &preview
	}
	resource.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return aiapplication.DraftPatchReviewResource{}, aiapplication.ErrReviewInvalid
	}
	if resource.Attempts, err = readAIAttemptProjections(ctx, tx, resource.JobID); err != nil {
		return aiapplication.DraftPatchReviewResource{}, err
	}
	if resource.RetrievalEvidence, err = readAIRetrievalEvidence(ctx, tx, resource.JobID); err != nil {
		return aiapplication.DraftPatchReviewResource{}, err
	}
	conflicts, err := readAIPatchFreshness(ctx, tx, resource.Patch)
	if err != nil {
		return aiapplication.DraftPatchReviewResource{}, err
	}
	resource.Freshness = aicontract.Freshness{State: aicontract.FreshnessFresh}
	if len(conflicts) > 0 {
		resource.Freshness = aicontract.Freshness{State: aicontract.FreshnessStale, ConflictingTarget: conflicts}
	}
	if decision, found, decisionErr := loadAIDecisionByPatch(ctx, tx, s.projectID, patchID); decisionErr != nil {
		return aiapplication.DraftPatchReviewResource{}, decisionErr
	} else if found {
		resource.Decision = &decision
		if decision.AcceptedRevisionID.Valid() {
			resource.Formal, err = readAIFormalLinks(ctx, tx, s.projectID, decision.AcceptedRevisionID)
			if err != nil {
				return aiapplication.DraftPatchReviewResource{}, err
			}
		}
	}
	if !resource.Valid() {
		return aiapplication.DraftPatchReviewResource{}, aiapplication.ErrReviewInvalid
	}
	if err = tx.Commit(); err != nil {
		return aiapplication.DraftPatchReviewResource{}, err
	}
	return resource, nil
}

func readAIAttemptProjections(ctx context.Context, tx *sql.Tx, jobID domain.ID) ([]aiapplication.AttemptReadProjection, error) {
	rows, err := tx.QueryContext(ctx, `SELECT a.attempt_id,a.ordinal,a.parent_attempt_id,a.repair_round,a.manifest_hash,o.outcome,
		EXISTS(SELECT 1 FROM ai_attempt_patch_seals s WHERE s.attempt_id=a.attempt_id)
		FROM ai_attempts a LEFT JOIN ai_attempt_outcomes o ON o.attempt_id=a.attempt_id
		WHERE a.job_id=? ORDER BY a.ordinal,a.attempt_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []aiapplication.AttemptReadProjection{}
	for rows.Next() {
		var value aiapplication.AttemptReadProjection
		var parent, outcome sql.NullString
		var manifestHash aicontract.Hash
		var sealed bool
		if err = rows.Scan(&value.ID, &value.Ordinal, &parent, &value.RepairRound, &manifestHash, &outcome, &sealed); err != nil {
			return nil, err
		}
		value.ParentAttemptID = aicontract.AttemptID(parent.String)
		value.Stage = aicontract.StageProviderToolLoop
		if sealed {
			value.Stage = aicontract.StagePatchSealed
		}
		value.Outcome = aicontract.OutcomeRunning
		if outcome.Valid {
			value.Outcome = aicontract.AttemptOutcome(outcome.String)
		}
		value.Manifest = aicontract.VersionIdentity{ID: "attempt-manifest", Version: "v1", Hash: manifestHash}
		values = append(values, value)
	}
	return values, rows.Err()
}

func readAIRetrievalEvidence(ctx context.Context, tx *sql.Tx, jobID domain.ID) ([]retrieval.EvidenceRefV1, error) {
	rows, err := tx.QueryContext(ctx, `SELECT canonical_evidence FROM ai_evidence_refs WHERE job_id=? ORDER BY ordinal`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []retrieval.EvidenceRefV1{}
	for rows.Next() {
		var canonical []byte
		var value retrieval.EvidenceRefV1
		if err = rows.Scan(&canonical); err != nil || json.Unmarshal(canonical, &value) != nil {
			return nil, aiapplication.ErrReviewInvalid
		}
		values = append(values, value)
	}
	return values, rows.Err()
}
