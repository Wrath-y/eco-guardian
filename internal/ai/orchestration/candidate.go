package orchestration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipatch "github.com/zouyi/eco-guardian/internal/ai/patch"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

var ErrCandidateInvalid = errors.New("AI candidate response is invalid")

// Candidate contains no unredacted Provider bytes. Canonical Patch and diff
// values are the only proposal inputs exposed to later deterministic stages.
type Candidate struct {
	Patch          aicontract.DraftPatchV1
	PatchCanonical []byte
	Diff           aicontract.DraftDiff
	DiffCanonical  []byte
	DiffHash       aicontract.Hash
	RawAudit       aiaudit.SealedProviderResponse
}

func BuildCandidate(response aiprovider.StructuredResponse, scope aipatch.DecodeContext, redactor aiaudit.Redactor) (Candidate, error) {
	if !response.Valid() || response.Schema != scope.Input.Schema {
		return Candidate{}, ErrCandidateInvalid
	}
	bodyHash := sha256.Sum256(response.Body)
	if hex.EncodeToString(bodyHash[:]) != string(response.BodyHash) {
		return Candidate{}, ErrCandidateInvalid
	}
	// Validate the original shape first so redaction cannot hide an unknown or
	// duplicate member. This preliminary value never leaves this function.
	if _, _, err := aipatch.DecodeV1(response.Body, scope); err != nil {
		return Candidate{}, errors.Join(ErrCandidateInvalid, err)
	}
	rawAudit, err := redactor.SealProviderResponse(response, scope.Input.Budget.MaxOutputBytes)
	if err != nil {
		return Candidate{}, errors.Join(ErrCandidateInvalid, err)
	}
	// Decode again from the bounded redacted copy. Thus rationale, assumptions,
	// and values containing a configured secret cannot enter the canonical
	// candidate or diff unredacted.
	value, canonical, err := aipatch.DecodeV1(rawAudit.StoredBody, scope)
	if err != nil {
		return Candidate{}, errors.Join(ErrCandidateInvalid, err)
	}
	diff, err := aipatch.BuildDiff(value, scope)
	if err != nil {
		return Candidate{}, errors.Join(ErrCandidateInvalid, err)
	}
	return Candidate{
		Patch: value, PatchCanonical: append([]byte(nil), canonical...), Diff: diff.Diff,
		DiffCanonical: append([]byte(nil), diff.Canonical...), DiffHash: diff.Hash, RawAudit: cloneRawAudit(rawAudit),
	}, nil
}

func cloneRawAudit(value aiaudit.SealedProviderResponse) aiaudit.SealedProviderResponse {
	value.StoredBody = append([]byte(nil), value.StoredBody...)
	return value
}
