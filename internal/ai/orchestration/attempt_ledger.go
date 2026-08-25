package orchestration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
)

var (
	ErrAttemptLedgerInvalid  = errors.New("AI attempt ledger fact is invalid")
	ErrAttemptLedgerConflict = errors.New("AI attempt ledger fact conflicts with an immutable fact")
)

type AttemptKind string

const (
	AttemptInitial AttemptKind = "initial"
	AttemptRepair  AttemptKind = "repair"
	AttemptRetry   AttemptKind = "explicit_retry"
)

type AttemptRecord struct {
	JobID        domain.ID                  `json:"job_id"`
	AttemptID    aicontract.AttemptID       `json:"attempt_id"`
	Kind         AttemptKind                `json:"kind"`
	Ordinal      int                        `json:"ordinal"`
	RepairRound  int                        `json:"repair_round"`
	ParentJobID  domain.ID                  `json:"parent_job_id,omitempty"`
	ParentID     aicontract.AttemptID       `json:"parent_attempt_id,omitempty"`
	Manifest     aiprovider.AttemptManifest `json:"manifest"`
	ManifestHash aicontract.Hash            `json:"manifest_hash"`
}

func NewAttemptRecord(jobID domain.ID, kind AttemptKind, ordinal, repairRound int, parentJobID domain.ID, parentID aicontract.AttemptID, manifest aiprovider.AttemptManifest) (AttemptRecord, error) {
	record := AttemptRecord{JobID: jobID, AttemptID: manifest.AttemptID, Kind: kind, Ordinal: ordinal, RepairRound: repairRound, ParentJobID: parentJobID, ParentID: parentID, Manifest: cloneLedgerManifest(manifest)}
	hash, err := hashLedger("eco-guardian.ai-attempt-manifest/v1", record.Manifest)
	if err != nil {
		return AttemptRecord{}, ErrAttemptLedgerInvalid
	}
	record.ManifestHash = aicontract.Hash(hash)
	if !record.Valid() {
		return AttemptRecord{}, ErrAttemptLedgerInvalid
	}
	return record, nil
}

func (record AttemptRecord) Valid() bool {
	if !record.JobID.Valid() || !record.AttemptID.Valid() || record.AttemptID != record.Manifest.AttemptID || !record.Manifest.Valid() || !record.ManifestHash.Valid() || record.Ordinal < 1 || record.RepairRound < 0 || record.ParentID == record.AttemptID {
		return false
	}
	hash, err := hashLedger("eco-guardian.ai-attempt-manifest/v1", record.Manifest)
	if err != nil || hash != string(record.ManifestHash) {
		return false
	}
	switch record.Kind {
	case AttemptInitial:
		return record.Ordinal == 1 && record.RepairRound == 0 && record.ParentJobID == "" && record.ParentID == ""
	case AttemptRepair:
		return record.Ordinal > 1 && record.RepairRound > 0 && record.ParentJobID == "" && record.ParentID.Valid()
	case AttemptRetry:
		return record.Ordinal == 1 && record.RepairRound == 0 && record.ParentJobID.Valid() && record.ParentJobID != record.JobID && record.ParentID.Valid()
	default:
		return false
	}
}

type TerminalResponseReceipt struct {
	JobID       domain.ID                      `json:"job_id"`
	AttemptID   aicontract.AttemptID           `json:"attempt_id"`
	Response    aiaudit.SealedProviderResponse `json:"response"`
	ReceiptHash aicontract.Hash                `json:"receipt_hash"`
}

func NewTerminalResponseReceipt(jobID domain.ID, attemptID aicontract.AttemptID, response aiaudit.SealedProviderResponse) (TerminalResponseReceipt, error) {
	receipt := TerminalResponseReceipt{JobID: jobID, AttemptID: attemptID, Response: cloneSealedResponse(response)}
	hash, err := hashLedger("eco-guardian.ai-terminal-response/v1", struct {
		JobID     domain.ID                      `json:"job_id"`
		AttemptID aicontract.AttemptID           `json:"attempt_id"`
		Response  aiaudit.SealedProviderResponse `json:"response"`
	}{jobID, attemptID, receipt.Response})
	if err != nil {
		return TerminalResponseReceipt{}, ErrAttemptLedgerInvalid
	}
	receipt.ReceiptHash = aicontract.Hash(hash)
	if !receipt.Valid() {
		return TerminalResponseReceipt{}, ErrAttemptLedgerInvalid
	}
	return receipt, nil
}

func (receipt TerminalResponseReceipt) Valid() bool {
	if !receipt.JobID.Valid() || !receipt.AttemptID.Valid() || !receipt.ReceiptHash.Valid() || !validSealedResponse(receipt.Response) {
		return false
	}
	hash, err := hashLedger("eco-guardian.ai-terminal-response/v1", struct {
		JobID     domain.ID                      `json:"job_id"`
		AttemptID aicontract.AttemptID           `json:"attempt_id"`
		Response  aiaudit.SealedProviderResponse `json:"response"`
	}{receipt.JobID, receipt.AttemptID, receipt.Response})
	return err == nil && hash == string(receipt.ReceiptHash)
}

type AttemptOutcomeReceipt struct {
	JobID       domain.ID                 `json:"job_id"`
	AttemptID   aicontract.AttemptID      `json:"attempt_id"`
	Outcome     aicontract.AttemptOutcome `json:"outcome"`
	ErrorCode   string                    `json:"error_code,omitempty"`
	OutcomeHash aicontract.Hash           `json:"outcome_hash"`
}

func NewAttemptOutcomeReceipt(jobID domain.ID, attemptID aicontract.AttemptID, outcome aicontract.AttemptOutcome, errorCode string) (AttemptOutcomeReceipt, error) {
	receipt := AttemptOutcomeReceipt{JobID: jobID, AttemptID: attemptID, Outcome: outcome, ErrorCode: errorCode}
	hash, err := hashLedger("eco-guardian.ai-attempt-outcome/v1", struct {
		JobID     domain.ID                 `json:"job_id"`
		AttemptID aicontract.AttemptID      `json:"attempt_id"`
		Outcome   aicontract.AttemptOutcome `json:"outcome"`
		ErrorCode string                    `json:"error_code,omitempty"`
	}{jobID, attemptID, outcome, errorCode})
	if err != nil {
		return AttemptOutcomeReceipt{}, ErrAttemptLedgerInvalid
	}
	receipt.OutcomeHash = aicontract.Hash(hash)
	if !receipt.Valid() {
		return AttemptOutcomeReceipt{}, ErrAttemptLedgerInvalid
	}
	return receipt, nil
}

func (receipt AttemptOutcomeReceipt) Valid() bool {
	if !receipt.JobID.Valid() || !receipt.AttemptID.Valid() || !receipt.OutcomeHash.Valid() || !terminalAttemptOutcome(receipt.Outcome) || !validStableCode(receipt.ErrorCode, receipt.Outcome == aicontract.OutcomeSucceeded) {
		return false
	}
	hash, err := hashLedger("eco-guardian.ai-attempt-outcome/v1", struct {
		JobID     domain.ID                 `json:"job_id"`
		AttemptID aicontract.AttemptID      `json:"attempt_id"`
		Outcome   aicontract.AttemptOutcome `json:"outcome"`
		ErrorCode string                    `json:"error_code,omitempty"`
	}{receipt.JobID, receipt.AttemptID, receipt.Outcome, receipt.ErrorCode})
	return err == nil && hash == string(receipt.OutcomeHash)
}

type AttemptPatchSeal struct {
	JobID     domain.ID            `json:"job_id"`
	AttemptID aicontract.AttemptID `json:"attempt_id"`
	PatchID   aicontract.PatchID   `json:"patch_id"`
	PatchHash aicontract.Hash      `json:"patch_hash"`
	SealHash  aicontract.Hash      `json:"seal_hash"`
}

func NewAttemptPatchSeal(jobID domain.ID, attemptID aicontract.AttemptID, patchID aicontract.PatchID, patchHash aicontract.Hash) (AttemptPatchSeal, error) {
	seal := AttemptPatchSeal{JobID: jobID, AttemptID: attemptID, PatchID: patchID, PatchHash: patchHash}
	hash, err := hashLedger("eco-guardian.ai-attempt-patch-seal/v1", struct {
		JobID     domain.ID            `json:"job_id"`
		AttemptID aicontract.AttemptID `json:"attempt_id"`
		PatchID   aicontract.PatchID   `json:"patch_id"`
		PatchHash aicontract.Hash      `json:"patch_hash"`
	}{jobID, attemptID, patchID, patchHash})
	if err != nil {
		return AttemptPatchSeal{}, ErrAttemptLedgerInvalid
	}
	seal.SealHash = aicontract.Hash(hash)
	if !seal.Valid() {
		return AttemptPatchSeal{}, ErrAttemptLedgerInvalid
	}
	return seal, nil
}

func (seal AttemptPatchSeal) Valid() bool {
	if !seal.JobID.Valid() || !seal.AttemptID.Valid() || !seal.PatchID.Valid() || !domain.ID(seal.PatchID).Valid() || !seal.PatchHash.Valid() || !seal.SealHash.Valid() {
		return false
	}
	hash, err := hashLedger("eco-guardian.ai-attempt-patch-seal/v1", struct {
		JobID     domain.ID            `json:"job_id"`
		AttemptID aicontract.AttemptID `json:"attempt_id"`
		PatchID   aicontract.PatchID   `json:"patch_id"`
		PatchHash aicontract.Hash      `json:"patch_hash"`
	}{seal.JobID, seal.AttemptID, seal.PatchID, seal.PatchHash})
	return err == nil && hash == string(seal.SealHash)
}

// AttemptLedgerRepository stores each fact with a uniqueness key scoped to an
// attempt. Identical retries replay; differing second writes conflict.
type AttemptLedgerRepository interface {
	InsertAttempt(context.Context, AttemptRecord) (AttemptRecord, bool, error)
	InsertTerminalResponse(context.Context, TerminalResponseReceipt) (TerminalResponseReceipt, bool, error)
	InsertAttemptOutcome(context.Context, AttemptOutcomeReceipt) (AttemptOutcomeReceipt, bool, error)
	InsertAttemptPatchSeal(context.Context, AttemptPatchSeal) (AttemptPatchSeal, bool, error)
}

type AttemptLedger struct{ Repository AttemptLedgerRepository }

func (ledger AttemptLedger) Create(ctx context.Context, record AttemptRecord) (AttemptRecord, bool, error) {
	if ctx == nil || ledger.Repository == nil || !record.Valid() {
		return AttemptRecord{}, false, ErrAttemptLedgerInvalid
	}
	stored, replay, err := ledger.Repository.InsertAttempt(ctx, cloneAttemptRecord(record))
	if err != nil {
		return AttemptRecord{}, false, err
	}
	if !stored.Valid() || !equalAttemptRecord(stored, record) {
		return AttemptRecord{}, false, ErrAttemptLedgerConflict
	}
	return cloneAttemptRecord(stored), replay, nil
}

func (ledger AttemptLedger) RecordResponse(ctx context.Context, receipt TerminalResponseReceipt) (TerminalResponseReceipt, bool, error) {
	if ctx == nil || ledger.Repository == nil || !receipt.Valid() {
		return TerminalResponseReceipt{}, false, ErrAttemptLedgerInvalid
	}
	stored, replay, err := ledger.Repository.InsertTerminalResponse(ctx, cloneResponseReceipt(receipt))
	if err != nil {
		return TerminalResponseReceipt{}, false, err
	}
	if !stored.Valid() || !equalResponseReceipt(stored, receipt) {
		return TerminalResponseReceipt{}, false, ErrAttemptLedgerConflict
	}
	return cloneResponseReceipt(stored), replay, nil
}

func (ledger AttemptLedger) Complete(ctx context.Context, receipt AttemptOutcomeReceipt) (AttemptOutcomeReceipt, bool, error) {
	if ctx == nil || ledger.Repository == nil || !receipt.Valid() {
		return AttemptOutcomeReceipt{}, false, ErrAttemptLedgerInvalid
	}
	stored, replay, err := ledger.Repository.InsertAttemptOutcome(ctx, receipt)
	if err != nil {
		return AttemptOutcomeReceipt{}, false, err
	}
	if !stored.Valid() || stored != receipt {
		return AttemptOutcomeReceipt{}, false, ErrAttemptLedgerConflict
	}
	return stored, replay, nil
}

func (ledger AttemptLedger) SealPatch(ctx context.Context, seal AttemptPatchSeal) (AttemptPatchSeal, bool, error) {
	if ctx == nil || ledger.Repository == nil || !seal.Valid() {
		return AttemptPatchSeal{}, false, ErrAttemptLedgerInvalid
	}
	stored, replay, err := ledger.Repository.InsertAttemptPatchSeal(ctx, seal)
	if err != nil {
		return AttemptPatchSeal{}, false, err
	}
	if !stored.Valid() || stored != seal {
		return AttemptPatchSeal{}, false, ErrAttemptLedgerConflict
	}
	return stored, replay, nil
}

func terminalAttemptOutcome(value aicontract.AttemptOutcome) bool {
	return value == aicontract.OutcomeSucceeded || value == aicontract.OutcomeFailed || value == aicontract.OutcomeCanceled || value == aicontract.OutcomeInterrupted
}

func validStableCode(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	return !optional && value == strings.TrimSpace(value) && len(value) <= 128 && !strings.ContainsAny(value, "\x00\r\n")
}

func validSealedResponse(value aiaudit.SealedProviderResponse) bool {
	if !value.Schema.Valid() || !value.OriginalBodyHash.Valid() || !value.StoredBodyHash.Valid() || len(value.StoredBody) == 0 || len(value.StoredBody) > aicontract.V1MaxOutputBytes || !json.Valid(value.StoredBody) {
		return false
	}
	hash := sha256.Sum256(value.StoredBody)
	return hex.EncodeToString(hash[:]) == string(value.StoredBodyHash)
}

func hashLedger(hashDomain string, value any) (string, error) {
	canonical, err := domain.CanonicalJSON(value)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(hashDomain))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write(canonical)
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func cloneLedgerManifest(value aiprovider.AttemptManifest) aiprovider.AttemptManifest {
	value.Tools = append([]aicontract.VersionIdentity(nil), value.Tools...)
	return value
}

func cloneAttemptRecord(value AttemptRecord) AttemptRecord {
	value.Manifest = cloneLedgerManifest(value.Manifest)
	return value
}

func equalAttemptRecord(left, right AttemptRecord) bool {
	leftJSON, _ := domain.CanonicalJSON(left)
	rightJSON, _ := domain.CanonicalJSON(right)
	return bytes.Equal(leftJSON, rightJSON)
}

func cloneSealedResponse(value aiaudit.SealedProviderResponse) aiaudit.SealedProviderResponse {
	value.StoredBody = append([]byte(nil), value.StoredBody...)
	return value
}

func cloneResponseReceipt(value TerminalResponseReceipt) TerminalResponseReceipt {
	value.Response = cloneSealedResponse(value.Response)
	return value
}

func equalResponseReceipt(left, right TerminalResponseReceipt) bool {
	leftJSON, _ := domain.CanonicalJSON(left)
	rightJSON, _ := domain.CanonicalJSON(right)
	return bytes.Equal(leftJSON, rightJSON)
}
