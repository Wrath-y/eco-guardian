package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	versioning "github.com/zouyi/eco-guardian/internal/versioning"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

func TestReleaseIntentPersistsImmutableInputsAndUsesPhaseCAS(t *testing.T) {
	s := newStore(t)
	_, revision, err := s.Create(context.Background(), "tag", tagDraft("intent"))
	if err != nil {
		t.Fatal(err)
	}
	policyID := mustID(t)
	if _, err = s.db.Exec(`INSERT INTO release_policies(id,display_version,canonical_body,canonical_hash,created_at) VALUES(?,?,?,?,?)`, policyID, 99, `{}`, strings.Repeat("a", 64), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.CreateOrGetReleaseJob(context.Background(), versioningrelease.JobRequest{ProjectID: s.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "intent-1", RequestHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"gates":[]}`)
	now := time.Now().UTC()
	override := &versioningrelease.OverrideAudit{GateResultID: mustID(t), Reason: "measured impact accepted", Confirmed: true}
	intent := versioningrelease.Intent{ID: mustID(t), JobID: job.ID, CandidateRevisionID: revision.ID, PolicyID: policyID, GateManifest: manifest, GateManifestHash: versioning.SHA256(manifest), Confirmations: []versioningrelease.Confirmation{{Kind: versioningrelease.ConfirmationAcknowledgeWarning, Confirmed: true}, {Kind: versioningrelease.ConfirmationNumericOverride, Confirmed: true}}, Backup: versioningrelease.BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("c", 64)}, RequestHash: job.RequestHash, IdempotencyKey: job.IdempotencyKey, Notes: "release after warning review", Override: override, Phase: versioningrelease.IntentRecorded, CreatedAt: now, UpdatedAt: now}
	created, existed, err := s.CreateIntent(context.Background(), intent)
	if err != nil || existed || created.ID != intent.ID {
		t.Fatalf("created=%#v existed=%v err=%v", created, existed, err)
	}
	if replay, existed, replayErr := s.CreateIntent(context.Background(), intent); replayErr != nil || !existed || replay.ID != intent.ID {
		t.Fatalf("replay=%#v existed=%v err=%v", replay, existed, replayErr)
	}
	changed, swapped, err := s.TransitionIntent(context.Background(), intent.ID, versioningrelease.IntentRecorded, versioningrelease.IntentGraphActivating, "task-1", "")
	if err != nil || !swapped || changed.Phase != versioningrelease.IntentGraphActivating || changed.ExternalTaskID != "task-1" {
		t.Fatalf("changed=%#v swapped=%v err=%v", changed, swapped, err)
	}
	if _, swapped, err = s.TransitionIntent(context.Background(), intent.ID, versioningrelease.IntentRecorded, versioningrelease.IntentFailed, "", "failed"); err != nil || swapped {
		t.Fatalf("stale CAS swapped=%v err=%v", swapped, err)
	}
	if _, swapped, err = s.TransitionReleaseJob(context.Background(), job.ID, versioningrelease.JobQueued, versioningrelease.JobRunning, nil); err != nil || !swapped {
		t.Fatalf("job running swapped=%v err=%v", swapped, err)
	}
	if _, swapped, err = s.TransitionIntent(context.Background(), intent.ID, versioningrelease.IntentGraphActivating, versioningrelease.IntentGraphActivated, "task-1", ""); err != nil || !swapped {
		t.Fatalf("graph activated swapped=%v err=%v", swapped, err)
	}
	release, pointer, err := s.CommitActivatedIntent(context.Background(), intent.ID, 0)
	if err != nil || !release.Valid() || pointer.ReleaseID != release.ID || pointer.Generation != 1 {
		t.Fatalf("release=%#v pointer=%#v err=%v", release, pointer, err)
	}
	if _, _, err = s.CommitActivatedIntent(context.Background(), intent.ID, 1); !errors.Is(err, ErrReleaseCommitConflict) {
		t.Fatalf("replay commit=%v", err)
	}
	if _, err = s.db.Exec(`INSERT INTO releases(id,revision_id,policy_id,intent_id,notes,gate_evidence,confirmations,created_at) VALUES(?,?,?,?,?,?,?,?)`, mustID(t), revision.ID, policyID, intent.ID, "", string(manifest), `[]`, now.Format(time.RFC3339Nano)); err == nil {
		t.Fatal("one intent was allowed to create a second release")
	}
	var releaseCount int
	if err = s.db.QueryRow(`SELECT count(*) FROM releases WHERE intent_id=?`, intent.ID).Scan(&releaseCount); err != nil || releaseCount != 1 {
		t.Fatalf("release count for intent=%d err=%v, want 1", releaseCount, err)
	}
	if _, err = s.db.Exec(`INSERT INTO active_release_pointer(singleton,active_release_id,generation) VALUES(1,?,2)`, release.ID); err == nil {
		t.Fatal("second active-release pointer singleton was allowed")
	}
	if _, err = s.db.Exec(`INSERT INTO active_release_pointer(singleton,active_release_id,generation) VALUES(2,?,2)`, release.ID); err == nil {
		t.Fatal("non-singleton active-release pointer was allowed")
	}
	var pointerCount int
	if err = s.db.QueryRow(`SELECT count(*) FROM active_release_pointer`).Scan(&pointerCount); err != nil || pointerCount != 1 {
		t.Fatalf("active pointer rows=%d err=%v, want 1", pointerCount, err)
	}

	assertTableColumns(t, s, "releases", []string{
		"id", "revision_id", "policy_id", "baseline_release_id", "intent_id",
		"notes", "gate_evidence", "confirmations", "created_at", "override_audit",
	})
	assertTableColumns(t, s, "release_intents", []string{
		"id", "job_id", "candidate_revision_id", "baseline_release_id", "policy_id",
		"gate_manifest", "gate_manifest_hash", "confirmations", "backup_evidence",
		"previous_graph_identity", "previous_release_id", "request_hash", "idempotency_key",
		"external_task_id", "phase", "error_details", "created_at", "updated_at", "notes", "override_audit",
	})
	assertForeignKeyTargets(t, s, "releases", map[string]string{
		"revision_id":         "config_revisions",
		"policy_id":           "release_policies",
		"baseline_release_id": "releases",
		"intent_id":           "release_intents",
	})

	// The only manifest copied into the saga is Gate evidence for audit. The
	// revision remains a foreign-key reference; neither release table has an
	// entity/blob/config-manifest column to become a second config fact source.
	for _, column := range append(releaseAuditColumns, intentAuditColumns...) {
		for _, forbidden := range []string{"entity_blob", "entity_blobs", "config_manifest", "version_manifest", "revision_entities"} {
			if column == forbidden || strings.HasSuffix(column, "_"+forbidden) {
				t.Fatalf("%s unexpectedly stores copied configuration data in column %q", column, column)
			}
		}
	}
	var gateEvidence string
	if err = s.db.QueryRow(`SELECT gate_evidence FROM releases WHERE id=?`, release.ID).Scan(&gateEvidence); err != nil || gateEvidence != string(manifest) {
		t.Fatalf("release gate evidence=%q err=%v, want audit manifest %q", gateEvidence, err, manifest)
	}
	var notes, rawOverride string
	if err = s.db.QueryRow(`SELECT notes,override_audit FROM releases WHERE id=?`, release.ID).Scan(&notes, &rawOverride); err != nil || notes != intent.Notes || !strings.Contains(rawOverride, string(override.GateResultID)) || !strings.Contains(rawOverride, override.Reason) {
		t.Fatalf("release audit notes=%q override=%q err=%v", notes, rawOverride, err)
	}
}

func TestReleaseIntentInsertCrashStagesLeaveNoHalfIntent(t *testing.T) {
	for _, stage := range []string{"release_intent_before_insert", "release_intent_after_insert"} {
		t.Run(stage, func(t *testing.T) {
			s := newStore(t)
			_, revision, err := s.Create(context.Background(), "tag", tagDraft("intent"))
			if err != nil {
				t.Fatal(err)
			}
			policyID := mustID(t)
			if _, err = s.db.Exec(`INSERT INTO release_policies(id,display_version,canonical_body,canonical_hash,created_at) VALUES(?,?,?,?,?)`, policyID, 102, `{}`, strings.Repeat("a", 64), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				t.Fatal(err)
			}
			job, _, err := s.CreateOrGetReleaseJob(context.Background(), versioningrelease.JobRequest{ProjectID: s.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: stage, RequestHash: strings.Repeat("b", 64)})
			if err != nil {
				t.Fatal(err)
			}
			manifest := []byte(`{"gates":[]}`)
			now := time.Now().UTC()
			intent := versioningrelease.Intent{ID: mustID(t), JobID: job.ID, CandidateRevisionID: revision.ID, PolicyID: policyID, GateManifest: manifest, GateManifestHash: versioning.SHA256(manifest), Backup: versioningrelease.BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("c", 64)}, RequestHash: job.RequestHash, IdempotencyKey: job.IdempotencyKey, Phase: versioningrelease.IntentRecorded, CreatedAt: now, UpdatedAt: now}
			s.failStage = func(at string) error {
				if at == stage {
					return errors.New("injected " + stage)
				}
				return nil
			}
			if _, _, err := s.CreateIntent(context.Background(), intent); err == nil {
				t.Fatal("injected intent crash unexpectedly succeeded")
			}
			var count int
			if err := s.db.QueryRow(`SELECT count(*) FROM release_intents WHERE job_id=?`, job.ID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("intent count=%d err=%v", count, err)
			}
		})
	}
}

var releaseAuditColumns = []string{
	"id", "revision_id", "policy_id", "baseline_release_id", "intent_id",
	"notes", "gate_evidence", "confirmations", "created_at", "override_audit",
}

var intentAuditColumns = []string{
	"id", "job_id", "candidate_revision_id", "baseline_release_id", "policy_id",
	"gate_manifest", "gate_manifest_hash", "confirmations", "backup_evidence",
	"previous_graph_identity", "previous_release_id", "request_hash", "idempotency_key",
	"external_task_id", "phase", "error_details", "created_at", "updated_at", "notes", "override_audit",
}

func assertTableColumns(t *testing.T, s *Store, table string, want []string) {
	t.Helper()
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s columns=%v, want %v", table, got, want)
	}
}

func assertForeignKeyTargets(t *testing.T, s *Store, table string, want map[string]string) {
	t.Helper()
	rows, err := s.db.Query(`PRAGMA foreign_key_list(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var id, sequence int
		var target, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &sequence, &target, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		got[from] = target
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("%s foreign keys=%v, want %v", table, got, want)
	}
	for column, target := range want {
		if got[column] != target {
			t.Fatalf("%s foreign key %s=%q, want %q", table, column, got[column], target)
		}
	}
}
