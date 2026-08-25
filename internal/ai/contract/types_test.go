package contract

import (
	"encoding/json"
	"testing"
)

const testHash = Hash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

func TestTaggedStatesRejectUnknownValues(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"provider", ProviderAvailable.Valid()},
		{"capability", CapabilityDegraded.Valid()},
		{"endpoint", EndpointLoopback.Valid()},
		{"baseline", BaselineNone.Valid()},
		{"metric", MetricTarget.Valid()},
		{"constraint", ConstraintRange.Valid()},
		{"operation", OperationReplace.Valid()},
		{"evidence", EvidenceRetrieval.Valid()},
		{"stage", StagePatchSealed.Valid()},
		{"outcome", OutcomeInterrupted.Valid()},
		{"freshness", FreshnessStale.Valid()},
		{"decision", DecisionAccepted.Valid()},
		{"unknown provider", ProviderState("ready").Valid()},
		{"unknown stage", AttemptStage("published").Valid()},
		{"unknown operation", PatchOperationKind("release").Valid()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want := test.name != "unknown provider" && test.name != "unknown stage" && test.name != "unknown operation"
			if test.valid != want {
				t.Fatalf("valid=%v want %v", test.valid, want)
			}
		})
	}
}

func TestFrozenIdentitiesAndScopeValidate(t *testing.T) {
	base := validBase()
	if !base.Valid() {
		t.Fatal("valid frozen base was rejected")
	}
	base.GraphSnapshot = "active"
	if base.Valid() {
		t.Fatal("graph snapshot must equal the frozen base revision")
	}
	if !(BaselineIdentity{Kind: BaselineNone}).Valid() {
		t.Fatal("tagged NO_BASELINE was rejected")
	}
	if (BaselineIdentity{Kind: BaselineNone, ReleaseID: "release-1"}).Valid() {
		t.Fatal("NO_BASELINE must not carry release identity")
	}
	if !FieldPath("/stats/damage~1base").Valid() || FieldPath("stats/damage").Valid() || FieldPath("/stats/~2bad").Valid() {
		t.Fatal("field path JSON-pointer validation drifted")
	}
	allowed := validAllowedTarget()
	if !allowed.Valid() {
		t.Fatal("valid target scope was rejected")
	}
	allowed.Paths = append(allowed.Paths, allowed.Paths[0])
	if allowed.Valid() {
		t.Fatal("duplicate target paths must be rejected")
	}
}

func TestAIDesignInputV1RequiresFrozenBoundedInput(t *testing.T) {
	input := validInput()
	if !input.Valid() {
		t.Fatal("valid design input was rejected")
	}
	duplicate := input
	duplicate.AllowedTargets = append(duplicate.AllowedTargets, duplicate.AllowedTargets[0])
	if duplicate.Valid() {
		t.Fatal("duplicate targets must be rejected")
	}
	unbounded := input
	unbounded.Budget.RetrievalResultLimit = 101
	if unbounded.Valid() {
		t.Fatal("retrieval provider maximum must be enforced")
	}
	nonCanonical := input
	nonCanonical.Metrics = []MetricGoal{{MetricID: "damage", Version: "1", Direction: MetricTarget, Target: "1.0", Unit: "points"}}
	if nonCanonical.Valid() {
		t.Fatal("non-canonical decimal target must be rejected")
	}
}

func TestToolPatchPreviewFreshnessAndDecisionValidation(t *testing.T) {
	evidence := EvidenceRef{ID: "evidence-1", Kind: EvidenceRetrieval, ManifestHash: testHash}
	call := ToolCall{ID: "call-1", AttemptID: "attempt-1", Ordinal: 1, Tool: version("preview_simulation"), Base: validBase(), InputHash: testHash}
	if !call.Valid() {
		t.Fatal("valid tool call was rejected")
	}
	result := ToolResult{CallID: call.ID, ImplementationVersion: "simulation-preview-v1", ResultHash: testHash, Evidence: []EvidenceRef{evidence}}
	if !result.Valid() {
		t.Fatal("valid tool result was rejected")
	}
	patch := validPatch()
	if !patch.Valid() {
		t.Fatal("valid draft patch was rejected")
	}
	missingEvidence := patch
	missingEvidence.Targets[0].Operations = append([]DraftOperation(nil), patch.Targets[0].Operations...)
	missingEvidence.Targets[0].Operations[0].Evidence = nil
	if missingEvidence.Valid() {
		t.Fatal("operation without evidence must be rejected")
	}
	preview := Preview{Advisory: true, InputHash: testHash, ResultHash: testHash, Evaluators: []VersionIdentity{version("simulation")}, Evidence: []EvidenceRef{evidence}, Acceptable: true}
	if !preview.Valid() {
		t.Fatal("valid advisory preview was rejected")
	}
	preview.Advisory = false
	if preview.Valid() {
		t.Fatal("formal result must not masquerade as AI preview")
	}
	if !(Freshness{State: FreshnessFresh}).Valid() || !(Freshness{State: FreshnessStale, ConflictingTarget: []EntityID{"entity-1"}}).Valid() || (Freshness{State: FreshnessStale}).Valid() {
		t.Fatal("freshness validation drifted")
	}
	accepted := HumanDecision{ID: "decision-1", Kind: DecisionAccepted, Actor: "local-user", RequestHash: testHash, ResultHash: testHash, AcceptedRevisionID: "revision-2"}
	if !accepted.Valid() {
		t.Fatal("valid accepted decision was rejected")
	}
	accepted.AcceptedRevisionID = ""
	if accepted.Valid() {
		t.Fatal("accepted decision must pin its revision")
	}
}

func TestAttemptStagesAreForwardOrdered(t *testing.T) {
	stages := []AttemptStage{StageInputPinned, StageEvidencePinned, StageProviderToolLoop, StageDeterministicPreview, StagePatchSealed}
	for index, stage := range stages {
		if !stage.Valid() || stage.Order() != index {
			t.Fatalf("stage %q order=%d want %d", stage, stage.Order(), index)
		}
	}
	attempt := Attempt{ID: "attempt-1", Ordinal: 1, Stage: StageInputPinned, Outcome: OutcomeRunning, ManifestIdentity: version("attempt")}
	if !attempt.Valid() {
		t.Fatal("valid attempt was rejected")
	}
	attempt.ParentAttemptID = attempt.ID
	if attempt.Valid() {
		t.Fatal("attempt cannot parent itself")
	}
}

func validInput() AIDesignInputV1 {
	return AIDesignInputV1{
		Schema:           version("ai-design-input"),
		Base:             validBase(),
		Baseline:         BaselineIdentity{Kind: BaselineNone},
		Goals:            []Goal{{ID: "goal-1", Description: "Reduce encounter variance"}},
		Metrics:          []MetricGoal{{MetricID: "damage", Version: "1", Direction: MetricTarget, Target: "1", Unit: "points"}},
		Constraints:      []Constraint{{ID: "constraint-1", Path: "/stats/cost", Operator: ConstraintLessOrEqual, Value: json.RawMessage(`10`)}},
		AllowedTargets:   []AllowedTarget{validAllowedTarget()},
		Scenes:           []string{"scene-default"},
		Budget:           validBudget(),
		RequiredVersions: []VersionIdentity{version("validation"), version("simulation"), version("risk")},
	}
}

func validBase() FrozenBaseIdentity {
	return FrozenBaseIdentity{
		ProjectID:           "project-1",
		ConfigRevisionID:    "revision-1",
		ConfigHash:          testHash,
		VersionManifestHash: testHash,
		MaterializationHash: testHash,
		GraphNamespace:      "project-1",
		GraphSnapshot:       "revision-1",
		GraphContentHash:    testHash,
	}
}

func validAllowedTarget() AllowedTarget {
	return AllowedTarget{
		EntityID:              "entity-1",
		Kind:                  "unit",
		ExpectedEntityVersion: 3,
		Paths:                 []AllowedPath{{Path: "/stats/damage", Operations: []PatchOperationKind{OperationReplace}}},
	}
}

func validBudget() Budget {
	return Budget{
		Policy: version("budget"),
		BudgetLimits: BudgetLimits{
			MaxFormatRepairs:     3,
			MaxProviderTurns:     4,
			MaxToolCalls:         12,
			MaxSearchCandidates:  100,
			MaxDurationMillis:    60_000,
			MaxContextBytes:      64_000,
			MaxOutputBytes:       32_000,
			MaxToolResultBytes:   32_000,
			RetrievalSeedLimit:   20,
			RetrievalResultLimit: 20,
			RetrievalGraphDepth:  1,
		},
	}
}

func validPatch() DraftPatchV1 {
	return DraftPatchV1{
		ID:                       "patch-1",
		Schema:                   version("draft-patch"),
		Base:                     validBase(),
		EvidenceManifestIdentity: version("retrieval-evidence"),
		Targets: []DraftTarget{{
			EntityID:              "entity-1",
			Kind:                  "unit",
			ExpectedEntityVersion: 3,
			Operations:            []DraftOperation{{Ordinal: 1, Kind: OperationReplace, Path: "/stats/damage", Value: json.RawMessage(`12`), Evidence: []EvidenceID{"evidence-1"}}},
		}},
		Rationale:   "Align damage with the selected goal.",
		Assumptions: []string{"The fixed scene remains representative."},
		Hash:        testHash,
	}
}

func version(id string) VersionIdentity {
	return VersionIdentity{ID: id, Version: "v1", Hash: testHash}
}
