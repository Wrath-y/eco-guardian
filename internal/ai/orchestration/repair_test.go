package orchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aipatch "github.com/zouyi/eco-guardian/internal/ai/patch"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
	aitools "github.com/zouyi/eco-guardian/internal/ai/tools"
)

func TestClassifyRepairAllowsOnlyRepresentationAndSchemaFailures(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		reason     RepairReason
		repairable bool
	}{
		{"malformed patch", patchFailure(aipatch.ErrorMalformedJSON), RepairJSONRepresentation, true},
		{"schema", patchFailure(aipatch.ErrorSchemaViolation), RepairSchema, true},
		{"type", patchFailure(aipatch.ErrorTypeViolation), RepairSchema, true},
		{"decimal", patchFailure(aipatch.ErrorNumericViolation), RepairSchema, true},
		{"unit", patchFailure(aipatch.ErrorUnitViolation), RepairSchema, true},
		{"json syntax", &json.SyntaxError{Offset: 1}, RepairJSONRepresentation, true},
		{"json type", &json.UnmarshalTypeError{Value: "number", Type: nil}, RepairJSONRepresentation, true},
		{"redacted representation", aiaudit.ErrProviderPayloadInvalid, RepairJSONRepresentation, true},
		{"identity", patchFailure(aipatch.ErrorIdentityMismatch), TerminalIdentity, false},
		{"scope", patchFailure(aipatch.ErrorScopeViolation), TerminalScope, false},
		{"collection", patchFailure(aipatch.ErrorCollectionViolation), TerminalScope, false},
		{"evidence", patchFailure(aipatch.ErrorEvidenceViolation), TerminalEvidence, false},
		{"ambiguous", patchFailure(aipatch.ErrorArrayAmbiguous), TerminalSemanticBlock, false},
		{"noop", patchFailure(aipatch.ErrorNoChange), TerminalSemanticBlock, false},
		{"policy", &aitools.PolicyError{Code: aitools.PolicyPathDenied}, TerminalPolicy, false},
		{"block", &DeterministicBlockError{Code: "FULL_VALIDATION_BLOCK"}, TerminalSemanticBlock, false},
		{"retrieval", retrieval.ErrEvidenceInvalid, TerminalEvidence, false},
		{"snapshot", retrieval.ErrSnapshotIdentityMismatch, TerminalEvidence, false},
		{"budget", aiaudit.ErrProviderPayloadLimit, TerminalBudget, false},
		{"candidate dependency", ErrCandidateInvalid, TerminalDependency, false},
		{"unknown", errors.New("some transient-looking text"), TerminalUnknown, false},
		{"nil", nil, TerminalUnknown, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := ClassifyRepair(test.err)
			if decision.Reason != test.reason || decision.Repairable != test.repairable {
				t.Fatalf("decision=%#v", decision)
			}
		})
	}
}

func TestClassifyRepairPreservesTerminalDecisionThroughWrapping(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("candidate: %w", patchFailure(aipatch.ErrorEvidenceViolation)),
		fmt.Errorf("tool: %w", &aitools.PolicyError{Code: aitools.PolicyUnknownTool}),
		fmt.Errorf("preview: %w", &DeterministicBlockError{Code: "BLOCK"}),
	} {
		if decision := ClassifyRepair(err); decision.Repairable {
			t.Fatalf("wrapped terminal error became repairable: %v => %#v", err, decision)
		}
	}
}

func patchFailure(code aipatch.ErrorCode) error {
	return &aipatch.DecodeError{Code: code}
}
