package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aiaudit "github.com/zouyi/eco-guardian/internal/ai/audit"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	aipersistence "github.com/zouyi/eco-guardian/internal/ai/persistence"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestAIPersistenceCrashAtomicityAndCredentialFreeBackup(t *testing.T) {
	const secret = "ai-backup-credential-canary"
	store := newStore(t)
	entity, revision, err := store.Create(context.Background(), domain.KindTag, tagDraft("aiverification"))
	if err != nil {
		t.Fatal(err)
	}
	var revisionsBefore, releasesBefore int
	if err = store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsBefore); err != nil {
		t.Fatal(err)
	}
	if err = store.db.QueryRow(`SELECT count(*) FROM releases`).Scan(&releasesBefore); err != nil {
		t.Fatal(err)
	}
	input := sqliteAIInput(t, store, entity, revision)
	inputHash, _ := aicontract.HashAIDesignInputV1(input)
	controller := aiorchestration.AIJobController{Jobs: store, Redactor: aiaudit.NewRedactor([]byte(secret))}
	state, _, err := controller.Admit(context.Background(), input, "ai-verification")
	if err != nil {
		t.Fatal(err)
	}
	pinned := sqlitePinnedEvidence(t, input, "credential evidence "+secret)
	batch, err := aipersistence.NewEvidenceBatch(state.Job.ID, "", pinned, aiaudit.NewRedactor([]byte(secret)))
	if err != nil || bytes.Contains(batch.Evidence().Canonical, []byte(secret)) {
		t.Fatalf("unsafe evidence err=%v", err)
	}
	pinned = batch.Evidence()
	if _, err = store.db.Exec(`CREATE TRIGGER ai_test_evidence_fault BEFORE INSERT ON ai_evidence_refs BEGIN SELECT RAISE(ABORT,'injected evidence crash'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.InsertEvidenceBatch(context.Background(), batch); err == nil {
		t.Fatal("injected evidence crash unexpectedly committed")
	}
	for _, table := range []string{"ai_evidence_manifests", "ai_evidence_refs"} {
		var count int
		if err = store.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE job_id=?`, state.Job.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial %s count=%d err=%v", table, count, err)
		}
	}
	if _, err = store.db.Exec(`DROP TRIGGER ai_test_evidence_fault`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.InsertEvidenceBatch(context.Background(), batch); err != nil {
		t.Fatal(err)
	}

	attemptID := aicontract.AttemptID("018f9e40-0000-7000-8000-0000000002a2")
	attempt, err := aiorchestration.NewAttemptRecord(state.Job.ID, aiorchestration.AttemptInitial, 1, 0, "", "", sqliteAttemptManifest(inputHash, pinned.ManifestHash, attemptID))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.InsertAttempt(context.Background(), attempt); err != nil {
		t.Fatal(err)
	}
	redactor := aiaudit.NewRedactor([]byte(secret))
	contextEvent, err := aiaudit.NewAttemptContextEvent(redactor, 1, attempt.Manifest, providerParameters(), input)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AppendAuditEvent(context.Background(), contextEvent); err != nil {
		t.Fatal(err)
	}
	secretEvent, err := aiaudit.NewEventDraft(redactor, 2, attemptID, aiaudit.EventProviderError, map[string]any{"api_key": secret, "message": "failed " + secret, "reasoning": "hidden " + secret}, nil)
	if err != nil || bytes.Contains(secretEvent.Payload(), []byte(secret)) {
		t.Fatalf("unsafe event err=%v", err)
	}
	if _, _, err = store.AppendAuditEvent(context.Background(), secretEvent); err != nil {
		t.Fatal(err)
	}

	candidate := sqliteCandidate(t, input, entity, pinned)
	patchRecord := aipersistence.DraftPatchRecord{
		JobID: state.Job.ID, AttemptID: attemptID, InputHash: inputHash, Candidate: candidate,
		ValidationHash: aicontract.Hash(strings.Repeat("c", 64)), Preview: sqlitePreview(inputHash, true), PreviewIssues: []string{}, Acceptability: aipersistence.PatchAcceptable,
	}
	if _, err = store.db.Exec(`CREATE TRIGGER ai_test_patch_fault BEFORE INSERT ON ai_draft_patches BEGIN SELECT RAISE(ABORT,'injected patch crash'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.InsertDraftPatch(context.Background(), patchRecord); err == nil {
		t.Fatal("injected Patch crash unexpectedly committed")
	}
	for _, table := range []string{"ai_blobs", "ai_draft_patches"} {
		var count int
		if err = store.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial %s count=%d err=%v", table, count, err)
		}
	}
	if _, err = store.db.Exec(`DROP TRIGGER ai_test_patch_fault`); err != nil {
		t.Fatal(err)
	}
	if _, err = store.InsertDraftPatch(context.Background(), patchRecord); err != nil {
		t.Fatal(err)
	}

	var revisionsAfter, releasesAfter int
	if err = store.db.QueryRow(`SELECT count(*) FROM config_revisions`).Scan(&revisionsAfter); err != nil {
		t.Fatal(err)
	}
	if err = store.db.QueryRow(`SELECT count(*) FROM releases`).Scan(&releasesAfter); err != nil {
		t.Fatal(err)
	}
	if revisionsAfter != revisionsBefore || releasesAfter != releasesBefore {
		t.Fatalf("AI generation changed configuration/release facts revisions=%d/%d releases=%d/%d", revisionsBefore, revisionsAfter, releasesBefore, releasesAfter)
	}

	backupPath := filepath.Join(t.TempDir(), "ai-backup.db")
	if _, err = store.db.ExecContext(context.Background(), `VACUUM INTO ?`, backupPath); err != nil {
		t.Fatal(err)
	}
	backupBytes, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(backupBytes, []byte(secret)) || !bytes.Contains(backupBytes, []byte(aiaudit.Redacted)) {
		t.Fatal("backup contained a credential or omitted the redacted audit evidence")
	}
	backup, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var integrity string
	if err = backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("backup integrity=%q err=%v", integrity, err)
	}
	var patches, auditEvents int
	if err = backup.QueryRow(`SELECT (SELECT count(*) FROM ai_draft_patches),(SELECT count(*) FROM ai_audit_events)`).Scan(&patches, &auditEvents); err != nil || patches != 1 || auditEvents != 2 {
		t.Fatalf("backup patches=%d audit=%d err=%v", patches, auditEvents, err)
	}
}

func providerParameters() aiprovider.ModelParameters {
	return aiprovider.ModelParameters{Temperature: "0", TopP: "1"}
}
