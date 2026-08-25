package orchestration

import (
	"encoding/json"
	"errors"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aipatch "github.com/zouyi/eco-guardian/internal/ai/patch"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
	aitools "github.com/zouyi/eco-guardian/internal/ai/tools"
)

type RepairReason string

const (
	RepairJSONRepresentation RepairReason = "AI_REPAIR_JSON_REPRESENTATION"
	RepairSchema             RepairReason = "AI_REPAIR_SCHEMA"
	TerminalIdentity         RepairReason = "AI_TERMINAL_IDENTITY"
	TerminalScope            RepairReason = "AI_TERMINAL_SCOPE"
	TerminalEvidence         RepairReason = "AI_TERMINAL_EVIDENCE"
	TerminalPolicy           RepairReason = "AI_TERMINAL_POLICY"
	TerminalSemanticBlock    RepairReason = "AI_TERMINAL_SEMANTIC_BLOCK"
	TerminalBudget           RepairReason = "AI_TERMINAL_BUDGET"
	TerminalDependency       RepairReason = "AI_TERMINAL_DEPENDENCY"
	TerminalUnknown          RepairReason = "AI_TERMINAL_UNKNOWN"
)

type RepairDecision struct {
	Reason     RepairReason
	Repairable bool
}

type DeterministicBlockError struct{ Code string }

func (e *DeterministicBlockError) Error() string {
	if e == nil || e.Code == "" {
		return "deterministic proposal evaluation blocked"
	}
	return "deterministic proposal evaluation blocked: " + e.Code
}

// ClassifyRepair is the only automatic-repair admission policy. Unknown
// failures are terminal by default; callers may not infer repairability from
// text or a dependency's generic retryable flag.
func ClassifyRepair(err error) RepairDecision {
	if err == nil {
		return RepairDecision{Reason: TerminalUnknown}
	}
	var patchError *aipatch.DecodeError
	if errors.As(err, &patchError) {
		switch patchError.Code {
		case aipatch.ErrorMalformedJSON:
			return RepairDecision{Reason: RepairJSONRepresentation, Repairable: true}
		case aipatch.ErrorSchemaViolation, aipatch.ErrorTypeViolation, aipatch.ErrorNumericViolation, aipatch.ErrorUnitViolation:
			return RepairDecision{Reason: RepairSchema, Repairable: true}
		case aipatch.ErrorIdentityMismatch:
			return RepairDecision{Reason: TerminalIdentity}
		case aipatch.ErrorScopeViolation, aipatch.ErrorCollectionViolation:
			return RepairDecision{Reason: TerminalScope}
		case aipatch.ErrorEvidenceViolation:
			return RepairDecision{Reason: TerminalEvidence}
		case aipatch.ErrorArrayAmbiguous, aipatch.ErrorNoChange:
			return RepairDecision{Reason: TerminalSemanticBlock}
		default:
			return RepairDecision{Reason: TerminalUnknown}
		}
	}
	var syntaxError *json.SyntaxError
	var typeError *json.UnmarshalTypeError
	if errors.As(err, &syntaxError) || errors.As(err, &typeError) || errors.Is(err, aiaudit.ErrProviderPayloadInvalid) {
		return RepairDecision{Reason: RepairJSONRepresentation, Repairable: true}
	}
	var policyError *aitools.PolicyError
	if errors.As(err, &policyError) {
		return RepairDecision{Reason: TerminalPolicy}
	}
	var block *DeterministicBlockError
	if errors.As(err, &block) {
		return RepairDecision{Reason: TerminalSemanticBlock}
	}
	if errors.Is(err, retrieval.ErrEvidenceInvalid) || errors.Is(err, retrieval.ErrEvidenceConflict) ||
		errors.Is(err, retrieval.ErrSnapshotIdentityMismatch) || errors.Is(err, retrieval.ErrResponseFilterMismatch) {
		return RepairDecision{Reason: TerminalEvidence}
	}
	if errors.Is(err, aiaudit.ErrProviderPayloadLimit) {
		return RepairDecision{Reason: TerminalBudget}
	}
	if errors.Is(err, ErrCandidateInvalid) {
		return RepairDecision{Reason: TerminalDependency}
	}
	return RepairDecision{Reason: TerminalUnknown}
}
