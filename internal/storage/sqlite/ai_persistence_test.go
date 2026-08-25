package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	aipatch "github.com/zouyi/eco-guardian/internal/ai/patch"
	aipersistence "github.com/zouyi/eco-guardian/internal/ai/persistence"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

func sqlitePinnedEvidence(t *testing.T, input aicontract.AIDesignInputV1, citation string) retrieval.PinnedEvidence {
	t.Helper()
	body, err := os.ReadFile("../../../tests/contract/fixtures/local-rag-hybrid-graph-retrieval-v1/hybrid-response.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var response retrieval.Response
	if err = decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	response.Request = retrieval.Request{Base: input.Base, Query: "find evidence", Filters: retrieval.Filters{NodeTypes: []string{"kind"}}, Budget: input.Budget}
	response.ResolvedSnapshotVersion = input.Base.GraphSnapshot
	response.ContentHash = string(input.Base.GraphContentHash)
	response.Results[0].CitationText = citation
	pinned, err := retrieval.CanonicalizeEvidence(response)
	if err != nil || !pinned.Valid() {
		t.Fatalf("pinned=%#v err=%v", pinned, err)
	}
	return pinned
}

func sqliteAttemptManifest(inputHash, evidenceHash aicontract.Hash, attemptID aicontract.AttemptID) aiprovider.AttemptManifest {
	hash := aicontract.Hash(strings.Repeat("a", 64))
	fixture := aicontract.V1Fixture()
	tools := make([]aicontract.VersionIdentity, len(fixture.Tools))
	for index, tool := range fixture.Tools {
		tools[index] = tool.Identity
	}
	return aiprovider.AttemptManifest{
		AttemptID: attemptID, Provider: aicontract.VersionIdentity{ID: "provider", Version: "v1", Hash: hash}, Model: aicontract.VersionIdentity{ID: "model", Version: "v1", Hash: hash},
		EndpointClassification: aiprovider.EndpointLoopback, Prompt: fixture.Prompt.Identity, StructuredResponseSchema: fixture.PatchSchema.Identity,
		Tools: tools, Orchestrator: fixture.Orchestrator.Identity, Budget: fixture.Budget.Identity, InputHash: inputHash, EvidenceManifestHash: evidenceHash,
	}
}

func sqliteCandidate(t *testing.T, input aicontract.AIDesignInputV1, entity domain.Entity, pinned retrieval.PinnedEvidence) aiorchestration.Candidate {
	t.Helper()
	evidenceIDs := make([]aicontract.EvidenceID, len(pinned.Manifest.Evidence))
	for index, item := range pinned.Manifest.Evidence {
		evidenceIDs[index] = item.ID
	}
	scope, err := aipatch.BuildDecodeContext(
		"018f9e40-0000-7000-8000-000000000295", input,
		aicontract.VersionIdentity{ID: "retrieval-evidence", Version: retrieval.EvidenceManifestVersionV1, Hash: pinned.ManifestHash},
		evidenceIDs, mustDomainRegistry(t), []domain.Entity{entity},
	)
	if err != nil {
		t.Fatal(err)
	}
	target := input.AllowedTargets[0]
	body := map[string]any{
		"id": scope.PatchID, "schema": input.Schema, "base": input.Base, "evidence_manifest_identity": scope.EvidenceManifestIdentity,
		"targets": []any{map[string]any{
			"entity_id": target.EntityID, "kind": target.Kind, "expected_entity_version": target.ExpectedEntityVersion,
			"operations": []any{map[string]any{"ordinal": 1, "kind": "replace", "path": "/payload/category", "value": "mechanic", "evidence": evidenceIDs}},
		}},
		"rationale": "Evidence-backed update.", "assumptions": []string{"The pinned scene remains representative."},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	candidate, err := aiorchestration.BuildCandidate(aiprovider.StructuredResponse{Schema: input.Schema, Body: raw, BodyHash: aicontract.Hash(hex.EncodeToString(digest[:]))}, scope, aiaudit.NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func mustDomainRegistry(t *testing.T) *domain.Registry {
	t.Helper()
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestSQLiteAIPersistenceSealsEvidenceAttemptsEventsAndDraftPatch(t *testing.T) {
	store := newStore(t)
	entity, revision, err := store.Create(context.Background(), domain.KindTag, tagDraft("aipersistence"))
	if err != nil {
		t.Fatal(err)
	}
	input := sqliteAIInput(t, store, entity, revision)
	inputHash, _ := aicontract.HashAIDesignInputV1(input)
	controller := aiorchestration.AIJobController{Jobs: store}
	state, _, err := controller.Admit(context.Background(), input, "ai-persistence")
	if err != nil {
		t.Fatal(err)
	}

	pinned := sqlitePinnedEvidence(t, input, "alpha")
	evidenceBatch, err := aipersistence.NewEvidenceBatch(state.Job.ID, "", pinned, aiaudit.NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	pinned = evidenceBatch.Evidence()
	if replay, err := store.InsertEvidenceBatch(context.Background(), evidenceBatch); err != nil || replay {
		t.Fatalf("evidence replay=%v err=%v", replay, err)
	}
	if replay, err := store.InsertEvidenceBatch(context.Background(), evidenceBatch); err != nil || !replay {
		t.Fatalf("evidence replay=%v err=%v", replay, err)
	}
	changedEvidence, err := aipersistence.NewEvidenceBatch(state.Job.ID, "", sqlitePinnedEvidence(t, input, "changed citation"), aiaudit.NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertEvidenceBatch(context.Background(), changedEvidence); !errors.Is(err, aipersistence.ErrConflict) {
		t.Fatalf("changed evidence err=%v", err)
	}

	attemptID := aicontract.AttemptID("018f9e40-0000-7000-8000-000000000290")
	attempt, err := aiorchestration.NewAttemptRecord(state.Job.ID, aiorchestration.AttemptInitial, 1, 0, "", "", sqliteAttemptManifest(inputHash, pinned.ManifestHash, attemptID))
	if err != nil {
		t.Fatal(err)
	}
	ledger := aiorchestration.AttemptLedger{Repository: store}
	if _, replay, err := ledger.Create(context.Background(), attempt); err != nil || replay {
		t.Fatalf("attempt replay=%v err=%v", replay, err)
	}
	if _, replay, err := ledger.Create(context.Background(), attempt); err != nil || !replay {
		t.Fatalf("attempt replay=%v err=%v", replay, err)
	}
	trail := aiaudit.Trail{Repository: store}
	redactor := aiaudit.NewRedactor()
	contextDraft, err := aiaudit.NewAttemptContextEvent(redactor, 1, attempt.Manifest, aiprovider.ModelParameters{Temperature: "0", TopP: "1"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, replay, err := trail.Append(context.Background(), contextDraft); err != nil || replay {
		t.Fatalf("audit context replay=%v err=%v", replay, err)
	}
	if _, replay, err := trail.Append(context.Background(), contextDraft); err != nil || !replay {
		t.Fatalf("audit context replay=%v err=%v", replay, err)
	}

	state, _, _ = controller.Claim(context.Background(), state, "worker-1")
	state, _, _ = controller.Advance(context.Background(), state, aiorchestration.PhaseEvidencePinned)
	state, _, _ = controller.Advance(context.Background(), state, aiorchestration.PhaseProviderToolLoop)
	stream := aiorchestration.AIJobEventStream{Repository: store}
	tool := aicontract.V1Fixture().Tools[0].Identity
	for _, draft := range []aiorchestration.AIJobEventDraft{
		{JobID: state.Job.ID, EventKey: "provider-stage", Kind: aiorchestration.EventStage, Phase: aiorchestration.PhaseProviderToolLoop, Progress: 50, AttemptID: attemptID},
		{JobID: state.Job.ID, EventKey: "tool-call", Kind: aiorchestration.EventTool, Phase: aiorchestration.PhaseProviderToolLoop, Progress: 60, AttemptID: attemptID, Tool: &tool},
	} {
		if _, replay, err := stream.Publish(context.Background(), draft); err != nil || replay {
			t.Fatalf("event=%#v replay=%v err=%v", draft, replay, err)
		}
	}
	if events, err := stream.Replay(context.Background(), state.Job.ID, "1"); err != nil || len(events) != 1 || events[0].ID != "2" {
		t.Fatalf("events=%#v err=%v", events, err)
	}

	candidate := sqliteCandidate(t, input, entity, pinned)
	response, err := aiorchestration.NewTerminalResponseReceipt(state.Job.ID, attemptID, candidate.RawAudit)
	if err != nil {
		t.Fatal(err)
	}
	if _, replay, err := ledger.RecordResponse(context.Background(), response); err != nil || replay {
		t.Fatalf("response replay=%v err=%v", replay, err)
	}
	outcome, _ := aiorchestration.NewAttemptOutcomeReceipt(state.Job.ID, attemptID, aicontract.OutcomeSucceeded, "")
	if _, replay, err := ledger.Complete(context.Background(), outcome); err != nil || replay {
		t.Fatalf("outcome replay=%v err=%v", replay, err)
	}
	failed, _ := aiorchestration.NewAttemptOutcomeReceipt(state.Job.ID, attemptID, aicontract.OutcomeFailed, "AI_PROVIDER_FAILED")
	if _, _, err := ledger.Complete(context.Background(), failed); !errors.Is(err, aiorchestration.ErrAttemptLedgerConflict) {
		t.Fatalf("conflicting outcome err=%v", err)
	}

	patchRecord := aipersistence.DraftPatchRecord{
		JobID: state.Job.ID, AttemptID: attemptID, InputHash: inputHash, Candidate: candidate,
		ValidationHash: aicontract.Hash(strings.Repeat("c", 64)), PreviewHash: aicontract.Hash(strings.Repeat("d", 64)), Acceptability: aipersistence.PatchAcceptable,
	}
	if replay, err := store.InsertDraftPatch(context.Background(), patchRecord); err != nil || replay {
		t.Fatalf("patch replay=%v err=%v", replay, err)
	}
	if replay, err := store.InsertDraftPatch(context.Background(), patchRecord); err != nil || !replay {
		t.Fatalf("patch replay=%v err=%v", replay, err)
	}
	seal, err := aiorchestration.NewAttemptPatchSeal(state.Job.ID, attemptID, candidate.Patch.ID, candidate.Patch.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if _, replay, err := ledger.SealPatch(context.Background(), seal); err != nil || replay {
		t.Fatalf("seal replay=%v err=%v", replay, err)
	}

	state, _, _ = controller.Advance(context.Background(), state, aiorchestration.PhaseDeterministicPreview)
	state, _, _ = controller.Advance(context.Background(), state, aiorchestration.PhasePatchSealed)
	state, _, err = controller.SealPatch(context.Background(), state, candidate.Patch.ID)
	if err != nil || state.Job.Status != sharedjob.Succeeded {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	terminal := aiorchestration.AIJobEventDraft{JobID: state.Job.ID, EventKey: "terminal", Kind: aiorchestration.EventTerminal, Phase: aiorchestration.PhasePatchSealed, Progress: 100, AttemptID: attemptID, Outcome: aicontract.OutcomeSucceeded, Result: state.Job.Result}
	if _, replay, err := stream.Publish(context.Background(), terminal); err != nil || replay {
		t.Fatalf("terminal replay=%v err=%v", replay, err)
	}

	auditFacts := []struct {
		kind    aiaudit.EventKind
		payload any
	}{
		{aiaudit.EventEvidencePinned, pinned.Manifest},
		{aiaudit.EventProviderUsage, aiprovider.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
		{aiaudit.EventProviderWarning, aiprovider.Warning{Code: "AI_DEGRADED", Message: "retrieval degraded"}},
		{aiaudit.EventProviderError, aiprovider.TerminalError{Code: "AI_TIMEOUT", Class: aiprovider.ErrorTimeout, Retryable: true, Message: "bounded timeout"}},
		{aiaudit.EventStructuredResponse, response.Response},
		{aiaudit.EventToolCall, map[string]any{"tool": tool, "call_id": "call-1"}},
		{aiaudit.EventToolResult, map[string]any{"tool": tool, "result_hash": strings.Repeat("e", 64), "duration_millis": 4}},
		{aiaudit.EventPatch, candidate.Patch},
		{aiaudit.EventDiff, candidate.Diff},
		{aiaudit.EventPreview, map[string]any{"validation_hash": patchRecord.ValidationHash, "preview_hash": patchRecord.PreviewHash, "acceptable": true}},
		{aiaudit.EventExplanation, map[string]any{"rationale": candidate.Patch.Rationale, "assumptions": candidate.Patch.Assumptions}},
		{aiaudit.EventAttemptOutcome, outcome},
		{aiaudit.EventCancellation, map[string]any{"cancel_generation": 0, "observed": false}},
		{aiaudit.EventRetry, map[string]any{"kind": attempt.Kind, "repair_round": attempt.RepairRound, "parent_attempt_id": attempt.ParentID}},
		{aiaudit.EventHumanDecision, aicontract.HumanDecision{ID: "pending-review", Kind: aicontract.DecisionDiscarded, Actor: "local-user", RequestHash: inputHash, ResultHash: candidate.Patch.Hash}},
	}
	for index, fact := range auditFacts {
		draft, draftErr := aiaudit.NewEventDraft(redactor, index+2, attemptID, fact.kind, fact.payload, nil)
		if draftErr != nil {
			t.Fatalf("audit kind=%s draft err=%v", fact.kind, draftErr)
		}
		if _, replay, appendErr := trail.Append(context.Background(), draft); appendErr != nil || replay {
			t.Fatalf("audit kind=%s replay=%v err=%v", fact.kind, replay, appendErr)
		}
	}
	if events, err := store.ListAuditEvents(context.Background(), attemptID); err != nil || len(events) != len(auditFacts)+1 || events[len(events)-1].Record.EventType != string(aiaudit.EventHumanDecision) {
		t.Fatalf("audit events=%d err=%v", len(events), err)
	}
	conflict, _ := aiaudit.NewEventDraft(redactor, 1, attemptID, aiaudit.EventAttemptContext, map[string]any{"changed": true}, nil)
	if _, _, err := trail.Append(context.Background(), conflict); !errors.Is(err, aiaudit.ErrAuditEventConflict) {
		t.Fatalf("audit conflict err=%v", err)
	}

	for table, want := range map[string]int{"ai_evidence_manifests": 1, "ai_evidence_refs": 1, "ai_attempts": 1, "ai_attempt_responses": 1, "ai_attempt_outcomes": 1, "ai_draft_patches": 1, "ai_attempt_patch_seals": 1, "ai_blobs": 2, "ai_job_event_details": 3, "ai_audit_events": 16} {
		var count int
		if err := store.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}

	for name, statement := range map[string]string{
		"attempt update":           `UPDATE ai_attempts SET manifest_hash=lower(manifest_hash)`,
		"response delete":          `DELETE FROM ai_attempt_responses`,
		"outcome update":           `UPDATE ai_attempt_outcomes SET outcome_hash=lower(outcome_hash)`,
		"evidence manifest delete": `DELETE FROM ai_evidence_manifests`,
		"evidence ref update":      `UPDATE ai_evidence_refs SET evidence_hash=lower(evidence_hash)`,
		"blob delete":              `DELETE FROM ai_blobs`,
		"draft patch update":       `UPDATE ai_draft_patches SET patch_hash=lower(patch_hash)`,
		"patch seal delete":        `DELETE FROM ai_attempt_patch_seals`,
		"audit event update":       `UPDATE ai_audit_events SET event_hash=lower(event_hash)`,
		"event detail update":      `UPDATE ai_job_event_details SET event_hash=lower(event_hash)`,
		"shared event delete":      `DELETE FROM job_events WHERE job_id='` + string(state.Job.ID) + `'`,
	} {
		if _, err := store.db.Exec(statement); err == nil {
			t.Fatalf("sealed generation fact accepted %s", name)
		}
	}
}
