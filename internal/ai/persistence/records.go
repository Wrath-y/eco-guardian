// Package persistence defines bounded, transport-neutral generation records
// written below the AI application services.
package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	ErrInvalid  = errors.New("AI persistence record is invalid")
	ErrConflict = errors.New("AI persistence record conflicts with sealed data")
)

type EvidenceBatch struct {
	jobID     domain.ID
	attemptID aicontract.AttemptID
	evidence  retrieval.PinnedEvidence
}

func NewEvidenceBatch(jobID domain.ID, attemptID aicontract.AttemptID, evidence retrieval.PinnedEvidence, redactor aiaudit.Redactor) (EvidenceBatch, error) {
	redacted, err := retrieval.RedactPinnedEvidence(evidence, redactor)
	if err != nil {
		return EvidenceBatch{}, ErrInvalid
	}
	batch := EvidenceBatch{jobID: jobID, attemptID: attemptID, evidence: redacted}
	if !batch.Valid() {
		return EvidenceBatch{}, ErrInvalid
	}
	return batch, nil
}

func (batch EvidenceBatch) Valid() bool {
	return batch.jobID.Valid() && (batch.attemptID == "" || batch.attemptID.Valid()) && batch.evidence.Valid()
}

func (batch EvidenceBatch) JobID() domain.ID                   { return batch.jobID }
func (batch EvidenceBatch) AttemptID() aicontract.AttemptID    { return batch.attemptID }
func (batch EvidenceBatch) Evidence() retrieval.PinnedEvidence { return batch.evidence }

type PatchAcceptability string

const (
	PatchPending    PatchAcceptability = "pending"
	PatchAcceptable PatchAcceptability = "acceptable"
	PatchFailed     PatchAcceptability = "failed"
)

func (value PatchAcceptability) Valid() bool {
	return value == PatchPending || value == PatchAcceptable || value == PatchFailed
}

type DraftPatchRecord struct {
	JobID          domain.ID
	AttemptID      aicontract.AttemptID
	InputHash      aicontract.Hash
	Candidate      aiorchestration.Candidate
	ValidationHash aicontract.Hash
	PreviewHash    aicontract.Hash
	Acceptability  PatchAcceptability
}

func (record DraftPatchRecord) Valid() bool {
	if !record.JobID.Valid() || !record.AttemptID.Valid() || !record.InputHash.Valid() || !record.ValidationHash.Valid() || !record.PreviewHash.Valid() || !record.Acceptability.Valid() || !record.Candidate.Patch.Valid() || !record.Candidate.Diff.Valid() || record.Candidate.Diff.PatchHash != record.Candidate.Patch.Hash || record.Candidate.RawAudit.Schema != record.Candidate.Patch.Schema {
		return false
	}
	patchCanonical, err := aicontract.CanonicalDraftPatch(record.Candidate.Patch)
	if err != nil || !bytes.Equal(patchCanonical, record.Candidate.PatchCanonical) || len(patchCanonical) > 262_144 {
		return false
	}
	patchHash, err := aicontract.HashDraftPatch(record.Candidate.Patch)
	if err != nil || patchHash != record.Candidate.Patch.Hash {
		return false
	}
	diffCanonical, err := aicontract.CanonicalDraftDiff(record.Candidate.Diff)
	if err != nil || !bytes.Equal(diffCanonical, record.Candidate.DiffCanonical) || len(diffCanonical) > 1_048_576 {
		return false
	}
	diffHash, err := aicontract.HashDraftDiff(record.Candidate.Diff)
	if err != nil || diffHash != record.Candidate.DiffHash {
		return false
	}
	raw := record.Candidate.RawAudit
	if !raw.Schema.Valid() || !raw.OriginalBodyHash.Valid() || !raw.StoredBodyHash.Valid() || len(raw.StoredBody) == 0 || len(raw.StoredBody) > aicontract.V1MaxOutputBytes || !json.Valid(raw.StoredBody) {
		return false
	}
	digest := sha256.Sum256(raw.StoredBody)
	return hex.EncodeToString(digest[:]) == string(raw.StoredBodyHash)
}

type GenerationRepository interface {
	InsertEvidenceBatch(context.Context, EvidenceBatch) (bool, error)
	InsertDraftPatch(context.Context, DraftPatchRecord) (bool, error)
}
