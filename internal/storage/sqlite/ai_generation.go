package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipersistence "github.com/zouyi/eco-guardian/internal/ai/persistence"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var _ aipersistence.GenerationRepository = (*Store)(nil)

func (s *Store) InsertEvidenceBatch(ctx context.Context, batch aipersistence.EvidenceBatch) (bool, error) {
	if ctx == nil || !batch.Valid() {
		return false, aipersistence.ErrInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if replay, found, findErr := replayAIEvidenceBatch(ctx, tx, batch, s.projectID); findErr != nil {
		return false, findErr
	} else if found {
		if !replay {
			return false, aipersistence.ErrConflict
		}
		return true, tx.Commit()
	}
	if !aiRunExists(ctx, tx, batch.JobID(), s.projectID) || batch.AttemptID() != "" && !aiAttemptBelongsToJob(ctx, tx, batch.AttemptID(), batch.JobID(), s.projectID) {
		return false, aipersistence.ErrConflict
	}
	now := formatAIJobTime(s.now().UTC())
	evidence := batch.Evidence()
	canonicalRefs := make([][]byte, len(evidence.Manifest.Evidence))
	additionalBytes := len(evidence.Canonical)
	for index, record := range evidence.Manifest.Evidence {
		canonical, encodeErr := domain.CanonicalJSON(record)
		if encodeErr != nil || len(canonical) == 0 || len(canonical) > 32768 {
			return false, aipersistence.ErrInvalid
		}
		canonicalRefs[index] = canonical
		additionalBytes += len(canonical)
	}
	if err = ensureAIRunCapacity(ctx, tx, batch.JobID(), s.projectID, additionalBytes); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_evidence_manifests(job_id,attempt_id,manifest_hash,request_hash,response_hash,canonical_manifest,created_at) VALUES(?,?,?,?,?,?,?)`, batch.JobID(), nullableAttemptID(batch.AttemptID()), evidence.ManifestHash, evidence.Manifest.RequestHash, evidence.Manifest.ResponseHash, evidence.Canonical, now); err != nil {
		return false, err
	}
	for index, record := range evidence.Manifest.Evidence {
		if _, err = tx.ExecContext(ctx, `INSERT INTO ai_evidence_refs(job_id,ordinal,evidence_id,attempt_id,canonical_evidence,evidence_hash,created_at) VALUES(?,?,?,?,?,?,?)`, batch.JobID(), index+1, record.ID, nullableAttemptID(batch.AttemptID()), canonicalRefs[index], record.RecordHash, now); err != nil {
			return false, err
		}
	}
	return false, tx.Commit()
}

func (s *Store) InsertDraftPatch(ctx context.Context, record aipersistence.DraftPatchRecord) (bool, error) {
	if ctx == nil || !record.Valid() {
		return false, aipersistence.ErrInvalid
	}
	s.writes.Lock()
	defer s.writes.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	rawBlobHash := string(record.Candidate.RawAudit.StoredBodyHash)
	diffDigest := sha256.Sum256(record.Candidate.DiffCanonical)
	diffBlobHash := hex.EncodeToString(diffDigest[:])
	canonicalPreview, previewHash, err := record.PreviewFacts()
	if err != nil {
		return false, aipersistence.ErrInvalid
	}
	previewIssues, err := json.Marshal(record.PreviewIssues)
	if err != nil {
		return false, aipersistence.ErrInvalid
	}
	if replay, found, findErr := replayAIDraftPatch(ctx, tx, record, rawBlobHash, diffBlobHash, canonicalPreview, previewIssues, previewHash, s.projectID); findErr != nil {
		return false, findErr
	} else if found {
		if !replay {
			return false, aipersistence.ErrConflict
		}
		return true, tx.Commit()
	}
	if !aiAttemptBelongsToJob(ctx, tx, record.AttemptID, record.JobID, s.projectID) {
		return false, aipersistence.ErrConflict
	}
	var inputHash string
	var baseRevision domain.ID
	if err = tx.QueryRowContext(ctx, `SELECT input_hash,base_revision_id FROM ai_design_runs WHERE job_id=? AND project_uuid=?`, record.JobID, s.projectID).Scan(&inputHash, &baseRevision); err != nil || inputHash != string(record.InputHash) || baseRevision != domain.ID(record.Candidate.Patch.Base.ConfigRevisionID) {
		return false, aipersistence.ErrConflict
	}
	if err = ensureAIRunCapacity(ctx, tx, record.JobID, s.projectID, len(record.Candidate.PatchCanonical)+len(record.Candidate.RawAudit.StoredBody)+len(record.Candidate.DiffCanonical)+len(canonicalPreview)+len(previewIssues)); err != nil {
		return false, err
	}
	now := formatAIJobTime(s.now().UTC())
	if err = insertAIBlob(ctx, tx, rawBlobHash, "application/vnd.ecoguardian.ai-provider-redacted+json", record.Candidate.RawAudit.StoredBody, now); err != nil {
		return false, err
	}
	if err = insertAIBlob(ctx, tx, diffBlobHash, "application/vnd.ecoguardian.ai-draft-diff+json", record.Candidate.DiffCanonical, now); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ai_draft_patches(id,job_id,attempt_id,base_revision_id,input_hash,patch_hash,canonical_patch,raw_redacted_blob_hash,diff_hash,diff_blob_hash,validation_hash,preview_hash,acceptability,created_at,canonical_preview,preview_issues) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, record.Candidate.Patch.ID, record.JobID, record.AttemptID, record.Candidate.Patch.Base.ConfigRevisionID, record.InputHash, record.Candidate.Patch.Hash, record.Candidate.PatchCanonical, rawBlobHash, record.Candidate.DiffHash, diffBlobHash, record.ValidationHash, previewHash, record.Acceptability, now, canonicalPreview, previewIssues)
	if err != nil {
		return false, err
	}
	return false, tx.Commit()
}

func replayAIEvidenceBatch(ctx context.Context, tx *sql.Tx, batch aipersistence.EvidenceBatch, projectID domain.ID) (bool, bool, error) {
	var attempt sql.NullString
	var manifestHash, requestHash, responseHash string
	var canonical []byte
	err := tx.QueryRowContext(ctx, `SELECT m.attempt_id,m.manifest_hash,m.request_hash,m.response_hash,m.canonical_manifest FROM ai_evidence_manifests m JOIN ai_design_runs r ON r.job_id=m.job_id WHERE m.job_id=? AND r.project_uuid=?`, batch.JobID(), projectID).Scan(&attempt, &manifestHash, &requestHash, &responseHash, &canonical)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	evidence := batch.Evidence()
	if attempt.String != string(batch.AttemptID()) || manifestHash != string(evidence.ManifestHash) || requestHash != string(evidence.Manifest.RequestHash) || responseHash != string(evidence.Manifest.ResponseHash) || !bytes.Equal(canonical, evidence.Canonical) {
		return false, true, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT ordinal,evidence_id,canonical_evidence,evidence_hash FROM ai_evidence_refs WHERE job_id=? ORDER BY ordinal`, batch.JobID())
	if err != nil {
		return false, true, err
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var ordinal int
		var evidenceID, evidenceHash string
		var stored []byte
		if err = rows.Scan(&ordinal, &evidenceID, &stored, &evidenceHash); err != nil || index >= len(evidence.Manifest.Evidence) {
			return false, true, err
		}
		want := evidence.Manifest.Evidence[index]
		wantCanonical, _ := domain.CanonicalJSON(want)
		if ordinal != index+1 || evidenceID != string(want.ID) || evidenceHash != string(want.RecordHash) || !bytes.Equal(stored, wantCanonical) {
			return false, true, nil
		}
		index++
	}
	return index == len(evidence.Manifest.Evidence) && rows.Err() == nil, true, rows.Err()
}

func replayAIDraftPatch(ctx context.Context, tx *sql.Tx, record aipersistence.DraftPatchRecord, rawBlobHash, diffBlobHash string, canonicalPreview, previewIssues []byte, expectedPreviewHash aicontract.Hash, projectID domain.ID) (bool, bool, error) {
	var jobID domain.ID
	var attemptID, baseRevision, inputHash, patchHash, rawHash, diffHash, storedDiffBlob, validationHash, storedPreviewHash, acceptability string
	var canonical, storedPreview, storedIssues []byte
	err := tx.QueryRowContext(ctx, `SELECT p.job_id,p.attempt_id,p.base_revision_id,p.input_hash,p.patch_hash,p.canonical_patch,p.raw_redacted_blob_hash,p.diff_hash,p.diff_blob_hash,p.validation_hash,p.preview_hash,p.acceptability,p.canonical_preview,p.preview_issues FROM ai_draft_patches p JOIN ai_design_runs r ON r.job_id=p.job_id WHERE p.id=? AND r.project_uuid=?`, record.Candidate.Patch.ID, projectID).Scan(&jobID, &attemptID, &baseRevision, &inputHash, &patchHash, &canonical, &rawHash, &diffHash, &storedDiffBlob, &validationHash, &storedPreviewHash, &acceptability, &storedPreview, &storedIssues)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	match := jobID == record.JobID && attemptID == string(record.AttemptID) && baseRevision == string(record.Candidate.Patch.Base.ConfigRevisionID) && inputHash == string(record.InputHash) && patchHash == string(record.Candidate.Patch.Hash) && bytes.Equal(canonical, record.Candidate.PatchCanonical) && rawHash == rawBlobHash && diffHash == string(record.Candidate.DiffHash) && storedDiffBlob == diffBlobHash && validationHash == string(record.ValidationHash) && storedPreviewHash == string(expectedPreviewHash) && acceptability == string(record.Acceptability) && bytes.Equal(storedPreview, canonicalPreview) && bytes.Equal(storedIssues, previewIssues)
	return match, true, nil
}

func insertAIBlob(ctx context.Context, tx *sql.Tx, hash, mediaType string, body []byte, createdAt string) error {
	if len(body) == 0 || len(body) > 1_048_576 {
		return aipersistence.ErrInvalid
	}
	digest := sha256.Sum256(body)
	if hex.EncodeToString(digest[:]) != hash {
		return aipersistence.ErrInvalid
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO ai_blobs(content_hash,media_type,byte_size,body,created_at) VALUES(?,?,?,?,?)`, hash, mediaType, len(body), body, createdAt); err != nil {
		return err
	}
	var storedType string
	var storedSize int
	var storedBody []byte
	if err := tx.QueryRowContext(ctx, `SELECT media_type,byte_size,body FROM ai_blobs WHERE content_hash=?`, hash).Scan(&storedType, &storedSize, &storedBody); err != nil {
		return err
	}
	if storedType != mediaType || storedSize != len(body) || !bytes.Equal(storedBody, body) {
		return aipersistence.ErrConflict
	}
	return nil
}

func aiRunExists(ctx context.Context, tx *sql.Tx, jobID, projectID domain.ID) bool {
	var count int
	return tx.QueryRowContext(ctx, `SELECT count(*) FROM ai_design_runs WHERE job_id=? AND project_uuid=?`, jobID, projectID).Scan(&count) == nil && count == 1
}
