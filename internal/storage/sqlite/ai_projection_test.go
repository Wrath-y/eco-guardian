package sqlite

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	aipersistence "github.com/zouyi/eco-guardian/internal/ai/persistence"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestSQLiteDraftPatchReadProjectionComputesFreshnessAndFormalLinksWithoutHistoryWrites(t *testing.T) {
	store := newStore(t)
	entity, revision, err := store.Create(context.Background(), domain.KindTag, tagDraft("aiprojection"))
	if err != nil {
		t.Fatal(err)
	}
	input := sqliteAIInput(t, store, entity, revision)
	inputHash, _ := aicontract.HashAIDesignInputV1(input)
	controller := aiorchestration.AIJobController{Jobs: store}
	state, _, err := controller.Admit(context.Background(), input, "ai-projection")
	if err != nil {
		t.Fatal(err)
	}
	pinned := sqlitePinnedEvidence(t, input, "projection evidence")
	batch, err := aipersistence.NewEvidenceBatch(state.Job.ID, "", pinned, aiaudit.NewRedactor())
	if err != nil {
		t.Fatal(err)
	}
	pinned = batch.Evidence()
	if _, err = store.InsertEvidenceBatch(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
	attemptID := aicontract.AttemptID("018f9e40-0000-7000-8000-0000000002a1")
	attempt, err := aiorchestration.NewAttemptRecord(state.Job.ID, aiorchestration.AttemptInitial, 1, 0, "", "", sqliteAttemptManifest(inputHash, pinned.ManifestHash, attemptID))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.InsertAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	auditDraft, err := aiaudit.NewAttemptContextEvent(aiaudit.NewRedactor(), 1, attempt.Manifest, aiprovider.ModelParameters{Temperature: "0", TopP: "1"}, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AppendAuditEvent(context.Background(), auditDraft); err != nil {
		t.Fatal(err)
	}
	candidate := sqliteCandidate(t, input, entity, pinned)
	record := aipersistence.DraftPatchRecord{
		JobID: state.Job.ID, AttemptID: attemptID, InputHash: inputHash, Candidate: candidate,
		ValidationHash: aicontract.Hash(strings.Repeat("c", 64)), Preview: sqlitePreview(inputHash, true), PreviewIssues: []string{}, Acceptability: aipersistence.PatchAcceptable,
	}
	if _, err = store.InsertDraftPatch(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	review, err := store.ReadDraftPatchReview(context.Background(), candidate.Patch.ID)
	if err != nil || review.Patch.Hash != candidate.Patch.Hash || review.Preview == nil || !review.Preview.Acceptable || len(review.Attempts) != 1 || review.Attempts[0].Stage != aicontract.StageProviderToolLoop || len(review.RetrievalEvidence) != 1 || review.RetrievalEvidence[0].ID != pinned.Manifest.Evidence[0].ID || review.Decision != nil {
		t.Fatalf("review=%#v err=%v", review, err)
	}
	var historical, historicalAudit []byte
	if err = store.db.QueryRow(`SELECT canonical_patch FROM ai_draft_patches WHERE id=?`, candidate.Patch.ID).Scan(&historical); err != nil {
		t.Fatal(err)
	}
	if err = store.db.QueryRow(`SELECT canonical_event FROM ai_audit_events WHERE attempt_id=? AND ordinal=1`, attemptID).Scan(&historicalAudit); err != nil {
		t.Fatal(err)
	}

	fresh, err := store.ReadStoredPatchProjection(context.Background(), candidate.Patch.ID)
	if err != nil || fresh.Freshness.State != aicontract.FreshnessFresh || len(fresh.Freshness.ConflictingTarget) != 0 || fresh.Formal.Validation != "" || fresh.Formal.Graph != "" || fresh.Formal.Simulation != "" || fresh.Formal.Risk != "" {
		t.Fatalf("fresh=%#v err=%v", fresh, err)
	}
	_, acceptedRevision, err := store.Create(context.Background(), domain.KindTag, tagDraft("projectionadvance"))
	if err != nil {
		t.Fatal(err)
	}
	stale, err := store.ReadStoredPatchProjection(context.Background(), candidate.Patch.ID)
	if err != nil || stale.Freshness.State != aicontract.FreshnessStale || len(stale.Freshness.ConflictingTarget) != 1 || stale.Freshness.ConflictingTarget[0] != aicontract.EntityID(entity.ID) {
		t.Fatalf("stale=%#v err=%v", stale, err)
	}
	var after, afterAudit []byte
	if err = store.db.QueryRow(`SELECT canonical_patch FROM ai_draft_patches WHERE id=?`, candidate.Patch.ID).Scan(&after); err != nil || !bytes.Equal(after, historical) {
		t.Fatalf("historical patch changed err=%v", err)
	}
	if err = store.db.QueryRow(`SELECT canonical_event FROM ai_audit_events WHERE attempt_id=? AND ordinal=1`, attemptID).Scan(&afterAudit); err != nil || !bytes.Equal(afterAudit, historicalAudit) {
		t.Fatalf("historical audit changed err=%v", err)
	}

	decisionID, _ := domain.NewID()
	validationID, _ := domain.NewID()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err = store.db.Exec(`INSERT INTO ai_patch_decisions(id,patch_id,project_uuid,decision,idempotency_key,request_hash,patch_hash,expected_versions_hash,accepted_revision_id,decision_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		decisionID, candidate.Patch.ID, store.projectID, aicontract.DecisionAccepted, "projection-accept", inputHash, candidate.Patch.Hash, strings.Repeat("e", 64), acceptedRevision.ID, strings.Repeat("f", 64), now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO validation_runs(id,source_kind,source_revision_id,source_input_hash,scope,version_manifest_hash,version_manifest,status,error_count,block_count,warning_count,info_count,result_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		validationID, "revision", acceptedRevision.ID, strings.Repeat("1", 64), "FULL", strings.Repeat("2", 64), `{}`, "completed", 0, 0, 0, 0, strings.Repeat("3", 64), now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.db.Exec(`INSERT INTO graph_sync_states(revision_id,pipeline_state,generation,warnings,updated_at) VALUES(?,?,?,?,?)`, acceptedRevision.ID, "graph_ready", 0, `[]`, now); err != nil {
		t.Fatal(err)
	}
	linked, err := store.ReadStoredPatchProjection(context.Background(), candidate.Patch.ID)
	if err != nil || linked.AcceptedRevisionID != acceptedRevision.ID || linked.Formal.Validation != "/api/v1/validation/runs/"+string(validationID) || linked.Formal.Graph != "/api/v1/revisions/"+string(acceptedRevision.ID)+"/graph-status" || linked.Formal.Simulation != "" || linked.Formal.Risk != "" {
		t.Fatalf("linked=%#v err=%v", linked, err)
	}
}
