package validation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	aipreview "github.com/zouyi/eco-guardian/internal/ai/preview"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	corevalidation "github.com/zouyi/eco-guardian/internal/validation"
)

const (
	EvaluatorVersionV1   = "ai-proposal-validation-v1"
	PolicyNonOverridable = "non_overridable"
	PolicyAdvisory       = "advisory"
)

var ErrProposalValidationInvalid = errors.New("AI proposal validation is invalid")

// IssueV1 carries the original validator issue without accepting alternate
// model-authored severity, fingerprint, evidence, message or override fields.
type IssueV1 struct {
	Issue          corevalidation.Issue `json:"issue"`
	OverridePolicy string               `json:"override_policy"`
}

type ResultV1 struct {
	Version             string                         `json:"version"`
	Advisory            bool                           `json:"advisory"`
	MaterializationHash string                         `json:"materialization_hash"`
	Scope               corevalidation.Scope           `json:"scope"`
	Versions            corevalidation.VersionManifest `json:"versions"`
	Summary             corevalidation.SeveritySummary `json:"summary"`
	Issues              []IssueV1                      `json:"issues"`
	Canonical           []byte                         `json:"-"`
	Hash                string                         `json:"hash"`
}

// EvaluateV1 applies the same retained-v1 FULL analyzer used by formal
// validation, but only to a sealed isolated proposal materialization.
func EvaluateV1(ctx context.Context, proposal aipreview.ProposalMaterializationV1, schemas *domain.Registry, registry *formula.Registry, versions corevalidation.VersionManifest) (ResultV1, error) {
	if ctx == nil || !proposal.Valid() {
		return ResultV1{}, ErrProposalValidationInvalid
	}
	issues, err := corevalidation.AnalyzeV1(ctx, schemas, registry, versions, proposal.Entities, corevalidation.ScopeFull)
	if err != nil {
		return ResultV1{}, err
	}
	envelopes := make([]IssueV1, len(issues))
	for index, issue := range issues {
		envelopes[index] = IssueV1{Issue: issue, OverridePolicy: issuePolicy(issue.Severity)}
	}
	result := ResultV1{
		Version: EvaluatorVersionV1, Advisory: true, MaterializationHash: string(proposal.Hash), Scope: corevalidation.ScopeFull,
		Versions: versions, Summary: corevalidation.Summarize(issues), Issues: envelopes,
	}
	result.Canonical, result.Hash, err = canonicalResult(result)
	if err != nil || !result.Valid() {
		return ResultV1{}, ErrProposalValidationInvalid
	}
	return result, nil
}

func (result ResultV1) Valid() bool {
	if result.Version != EvaluatorVersionV1 || !result.Advisory || len(result.MaterializationHash) != 64 || result.Scope != corevalidation.ScopeFull || !result.Versions.Valid() || len(result.Hash) != 64 {
		return false
	}
	issues := make([]corevalidation.Issue, len(result.Issues))
	for index, envelope := range result.Issues {
		issue := envelope.Issue
		if !issue.Valid() || envelope.OverridePolicy != issuePolicy(issue.Severity) {
			return false
		}
		fingerprint, err := corevalidation.Fingerprint(result.Versions.Registry, issue)
		if err != nil || fingerprint != issue.Fingerprint {
			return false
		}
		issues[index] = issue
	}
	if result.Summary != corevalidation.Summarize(issues) {
		return false
	}
	canonical, hash, err := canonicalResult(result)
	return err == nil && hash == result.Hash && bytes.Equal(canonical, result.Canonical)
}

func issuePolicy(severity corevalidation.Severity) string {
	if severity == corevalidation.SeverityError || severity == corevalidation.SeverityBlock {
		return PolicyNonOverridable
	}
	return PolicyAdvisory
}

func canonicalResult(result ResultV1) ([]byte, string, error) {
	payload := struct {
		Version             string                         `json:"version"`
		Advisory            bool                           `json:"advisory"`
		MaterializationHash string                         `json:"materialization_hash"`
		Scope               corevalidation.Scope           `json:"scope"`
		Versions            corevalidation.VersionManifest `json:"versions"`
		Summary             corevalidation.SeveritySummary `json:"summary"`
		Issues              []IssueV1                      `json:"issues"`
	}{result.Version, result.Advisory, result.MaterializationHash, result.Scope, result.Versions, result.Summary, result.Issues}
	canonical, err := domain.CanonicalJSON(payload)
	if err != nil {
		return nil, "", err
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("eco-guardian.ai-proposal-validation/v1"))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(canonical)
	return canonical, hex.EncodeToString(hasher.Sum(nil)), nil
}
