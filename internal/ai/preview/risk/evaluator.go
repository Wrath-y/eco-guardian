package risk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aipreview "github.com/zouyi/eco-guardian/internal/ai/preview"
	"github.com/zouyi/eco-guardian/internal/domain"
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
	riskpreview "github.com/zouyi/eco-guardian/internal/risk/preview"
)

const VersionV1 = "ai-proposal-risk-v1"

var (
	ErrInvalid     = errors.New("AI proposal risk preview is invalid")
	ErrUnavailable = errors.New("AI proposal risk evaluator is unavailable")
)

type Request struct {
	Proposal aipreview.ProposalMaterializationV1 `json:"-"`
	Risk     riskpreview.Request                 `json:"risk"`
}

type ResultV1 struct {
	Version      string               `json:"version"`
	Advisory     bool                 `json:"advisory"`
	ProposalHash aicontract.Hash      `json:"proposal_materialization_hash"`
	Risk         riskpreview.ResultV1 `json:"risk"`
	Canonical    []byte               `json:"-"`
	Hash         string               `json:"hash"`
}

type Service struct{ Evaluator riskpreview.Evaluator }

func (service Service) Evaluate(ctx context.Context, request Request) (ResultV1, error) {
	if ctx == nil || service.Evaluator == nil {
		return ResultV1{}, ErrUnavailable
	}
	if !request.Proposal.Valid() || request.Risk.ProjectID != domain.ID(request.Proposal.Base.ProjectID) || request.Risk.ProposalMaterializationHash != string(request.Proposal.Hash) {
		return ResultV1{}, ErrInvalid
	}
	preview, err := service.Evaluator.Evaluate(ctx, request.Risk)
	if err != nil {
		return ResultV1{}, err
	}
	if !validPreview(preview, request.Proposal.Hash) {
		return ResultV1{}, ErrInvalid
	}
	result := ResultV1{Version: VersionV1, Advisory: true, ProposalHash: request.Proposal.Hash, Risk: preview}
	result.Canonical, result.Hash, err = canonicalResult(result)
	if err != nil || !result.Valid() {
		return ResultV1{}, ErrInvalid
	}
	return result, nil
}

func (result ResultV1) Valid() bool {
	if result.Version != VersionV1 || !result.Advisory || !result.ProposalHash.Valid() || !validHash(result.Hash) || !validPreview(result.Risk, result.ProposalHash) {
		return false
	}
	canonical, hash, err := canonicalResult(result)
	return err == nil && hash == result.Hash && bytes.Equal(canonical, result.Canonical)
}

func validPreview(value riskpreview.ResultV1, proposalHash aicontract.Hash) bool {
	if value.SchemaVersion != riskpreview.SchemaVersionV1 || !value.Advisory || value.ProposalMaterializationHash != string(proposalHash) || !validHash(value.InputHash) || !validHash(value.ResultHash) || len(value.RegistryManifests) == 0 {
		return false
	}
	copy := value
	copy.ResultHash = ""
	hash, _, err := riskcontract.CanonicalHash("eco-guardian/risk-preview-result/v1", copy)
	return err == nil && hash == value.ResultHash
}

func canonicalResult(result ResultV1) ([]byte, string, error) {
	payload := struct {
		Version      string               `json:"version"`
		Advisory     bool                 `json:"advisory"`
		ProposalHash aicontract.Hash      `json:"proposal_materialization_hash"`
		Risk         riskpreview.ResultV1 `json:"risk"`
	}{result.Version, result.Advisory, result.ProposalHash, result.Risk}
	canonical, err := domain.CanonicalJSON(payload)
	if err != nil {
		return nil, "", err
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte("eco-guardian.ai-proposal-risk/v1"))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(canonical)
	return canonical, hex.EncodeToString(hasher.Sum(nil)), nil
}

func validHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
