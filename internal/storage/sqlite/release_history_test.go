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

func TestReleaseHistoryReadsImmutableAuditAndRejectsInvalidCursor(t *testing.T) {
	s := newStore(t)
	_, revision, err := s.Create(context.Background(), "tag", tagDraft("releasehistory"))
	if err != nil {
		t.Fatal(err)
	}
	policyID := mustID(t)
	if _, err = s.db.Exec(`INSERT INTO release_policies(id,display_version,canonical_body,canonical_hash,created_at) VALUES(?,?,?,?,?)`, policyID, 100, `{}`, strings.Repeat("a", 64), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.CreateOrGetReleaseJob(context.Background(), versioningrelease.JobRequest{ProjectID: s.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "release-history", RequestHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"gates":[]}`)
	now := time.Now().UTC()
	intent := versioningrelease.Intent{ID: mustID(t), JobID: job.ID, CandidateRevisionID: revision.ID, PolicyID: policyID, GateManifest: manifest, GateManifestHash: versioning.SHA256(manifest), Confirmations: []versioningrelease.Confirmation{{Kind: versioningrelease.ConfirmationAcknowledgeWarning, Confirmed: true}}, Backup: versioningrelease.BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("c", 64)}, RequestHash: job.RequestHash, IdempotencyKey: job.IdempotencyKey, Notes: "immutable release audit", Override: &versioningrelease.OverrideAudit{GateResultID: mustID(t), Reason: "measured impact accepted", Confirmed: true}, Phase: versioningrelease.IntentRecorded, CreatedAt: now, UpdatedAt: now}
	if _, _, err = s.CreateIntent(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if _, swapped, transitionErr := s.TransitionReleaseJob(context.Background(), job.ID, versioningrelease.JobQueued, versioningrelease.JobRunning, nil); transitionErr != nil || !swapped {
		t.Fatalf("job running swapped=%v err=%v", swapped, transitionErr)
	}
	for _, transition := range [][2]versioningrelease.IntentPhase{{versioningrelease.IntentRecorded, versioningrelease.IntentGraphActivating}, {versioningrelease.IntentGraphActivating, versioningrelease.IntentGraphActivated}} {
		if _, swapped, transitionErr := s.TransitionIntent(context.Background(), intent.ID, transition[0], transition[1], "task", ""); transitionErr != nil || !swapped {
			t.Fatalf("intent transition=%v swapped=%v err=%v", transition, swapped, transitionErr)
		}
	}
	release, _, err := s.CommitActivatedIntent(context.Background(), intent.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.ListReleases(context.Background(), "", 50)
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != release.ID || page.Items[0].Notes != intent.Notes || page.Items[0].Override == nil || page.Items[0].Override.Reason != intent.Override.Reason || string(page.Items[0].GateEvidence) != string(manifest) {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	detail, err := s.GetReleaseRecord(context.Background(), release.ID)
	if err != nil || detail.ID != release.ID || detail.BaselineReleaseID != "" {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	if _, err = s.ListReleases(context.Background(), "not-a-cursor", 50); !errors.Is(err, ErrReleaseCursor) {
		t.Fatalf("invalid cursor error=%v", err)
	}
}
