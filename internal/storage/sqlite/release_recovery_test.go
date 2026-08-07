package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioning "github.com/zouyi/eco-guardian/internal/versioning"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

func TestProjectOpenRecoveryReplaysGraphAndCommitsOneRelease(t *testing.T) {
	s := newStore(t)
	_, revision, err := s.Create(context.Background(), "tag", tagDraft("recovery"))
	if err != nil {
		t.Fatal(err)
	}
	policyID := mustID(t)
	if _, err = s.db.Exec(`INSERT INTO release_policies(id,display_version,canonical_body,canonical_hash,created_at) VALUES(?,?,?,?,?)`, policyID, 101, `{}`, strings.Repeat("a", 64), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.CreateOrGetReleaseJob(context.Background(), versioningrelease.JobRequest{ProjectID: s.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "recovery-open", RequestHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	manifest := []byte(`{"gates":[{"result_id":"gate-1"}]}`)
	intent := versioningrelease.Intent{ID: mustID(t), JobID: job.ID, CandidateRevisionID: revision.ID, PolicyID: policyID, GateManifest: manifest, GateManifestHash: versioning.SHA256(manifest), Backup: versioningrelease.BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("c", 64)}, RequestHash: job.RequestHash, IdempotencyKey: job.IdempotencyKey, Phase: versioningrelease.IntentRecorded, CreatedAt: now, UpdatedAt: now}
	if _, existed, createErr := s.CreateIntent(context.Background(), intent); createErr != nil || existed {
		t.Fatalf("create intent existed=%v err=%v", existed, createErr)
	}
	if _, swapped, transitionErr := s.TransitionIntent(context.Background(), intent.ID, versioningrelease.IntentRecorded, versioningrelease.IntentGraphActivating, "graph-task", ""); transitionErr != nil || !swapped {
		t.Fatalf("intent graph activating swapped=%v err=%v", swapped, transitionErr)
	}
	if _, swapped, transitionErr := s.TransitionReleaseJob(context.Background(), job.ID, versioningrelease.JobQueued, versioningrelease.JobRunning, nil); transitionErr != nil || !swapped {
		t.Fatalf("job running swapped=%v err=%v", swapped, transitionErr)
	}
	if _, swapped, transitionErr := s.TransitionReleaseJob(context.Background(), job.ID, versioningrelease.JobRunning, versioningrelease.JobInterrupted, nil); transitionErr != nil || !swapped {
		t.Fatalf("job interrupted swapped=%v err=%v", swapped, transitionErr)
	}

	graph := &recoveryGraphFake{}
	recovery := versioningrelease.IntentRecovery{Intents: s, Jobs: s, Sources: s, Commit: s, Graph: graph}
	results, err := recovery.Recover(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != versioningrelease.RecoveryContinued {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	if len(graph.activations) != 1 || graph.activations[0].ProjectID != s.ProjectID() || graph.activations[0].RevisionID != revision.ID || graph.activations[0].IntentID != intent.ID {
		t.Fatalf("activations=%#v", graph.activations)
	}
	if len(graph.reads) != 1 || graph.reads[0] != (versioningrelease.GraphReadRequest{ProjectID: s.ProjectID(), RevisionID: revision.ID, ConfigHash: revision.ConfigHash}) {
		t.Fatalf("Graph reads=%#v", graph.reads)
	}
	pointer, err := s.GetActivePointer(context.Background())
	if err != nil || !pointer.ReleaseID.Valid() || pointer.Generation != 1 {
		t.Fatalf("pointer=%#v err=%v", pointer, err)
	}
	committed, err := s.GetIntent(context.Background(), intent.ID)
	if err != nil || committed.Phase != versioningrelease.IntentSucceeded {
		t.Fatalf("intent=%#v err=%v", committed, err)
	}
	completedJob, err := s.GetReleaseJob(context.Background(), job.ID)
	if err != nil || completedJob.Status != versioningrelease.JobSucceeded || completedJob.Result == nil || completedJob.Result.ID != pointer.ReleaseID {
		t.Fatalf("job=%#v err=%v", completedJob, err)
	}
	if pending, listErr := s.ListNonterminalReleaseIntents(context.Background()); listErr != nil || len(pending) != 0 {
		t.Fatalf("pending=%#v err=%v", pending, listErr)
	}
	if rerun, rerunErr := recovery.Recover(context.Background()); rerunErr != nil || len(rerun) != 0 {
		t.Fatalf("recovery replay=%#v err=%v", rerun, rerunErr)
	}
}

func TestReleaseCommitCrashStagesRollbackThenRecoverExactlyOneRelease(t *testing.T) {
	for _, stage := range []string{
		"release_commit_before_insert",
		"release_commit_after_release_insert",
		"release_commit_after_pointer_update",
		"release_commit_after_intent_commit",
	} {
		t.Run(stage, func(t *testing.T) {
			s := newStore(t)
			job, intent := activatedRecoveryIntent(t, s, "crash-"+stage)
			s.failStage = func(at string) error {
				if at == stage {
					return context.Canceled
				}
				return nil
			}
			if _, _, err := s.CommitActivatedIntent(context.Background(), intent.ID, 0); err == nil {
				t.Fatal("injected commit crash unexpectedly succeeded")
			}
			assertNoCommittedRelease(t, s, intent.ID)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, _, err := Open(filepath.Dir(s.path), s.registry)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			graph := &recoveryGraphFake{snapshot: versioningrelease.GraphSnapshot{ProjectID: job.ProjectID, RevisionID: job.RevisionID, ConfigHash: job.InputHash}}
			results, err := (versioningrelease.IntentRecovery{Intents: reopened, Jobs: reopened, Sources: reopened, Commit: reopened, Graph: graph}).Recover(context.Background())
			if err != nil || len(results) != 1 || results[0].Outcome != versioningrelease.RecoveryContinued {
				t.Fatalf("results=%#v err=%v", results, err)
			}
			pointer, pointerErr := reopened.GetActivePointer(context.Background())
			if pointerErr != nil || !pointer.ReleaseID.Valid() || pointer.Generation != 1 {
				t.Fatalf("pointer=%#v err=%v", pointer, pointerErr)
			}
			var releases int
			if err := reopened.db.QueryRow(`SELECT count(*) FROM releases WHERE intent_id=?`, intent.ID).Scan(&releases); err != nil || releases != 1 {
				t.Fatalf("releases=%d err=%v", releases, err)
			}
		})
	}
}

func TestRecoveryCompletesCommittedIntentWhoseJobDidNotFinishBeforeCrash(t *testing.T) {
	s := newStore(t)
	job, intent := activatedRecoveryIntent(t, s, "job-completion-crash")
	release, _, err := s.CommitActivatedIntent(context.Background(), intent.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := Open(filepath.Dir(s.path), s.registry)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	results, err := (versioningrelease.IntentRecovery{Intents: reopened, Jobs: reopened, Sources: reopened, Commit: reopened, Graph: &recoveryGraphFake{}}).Recover(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != versioningrelease.RecoveryCompleted {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	completed, err := reopened.GetReleaseJob(context.Background(), job.ID)
	if err != nil || completed.Status != versioningrelease.JobSucceeded || completed.Result == nil || completed.Result.ID != release.ID {
		t.Fatalf("job=%#v err=%v", completed, err)
	}
	var releases int
	if err := reopened.db.QueryRow(`SELECT count(*) FROM releases WHERE intent_id=?`, intent.ID).Scan(&releases); err != nil || releases != 1 {
		t.Fatalf("releases=%d err=%v", releases, err)
	}
}

func activatedRecoveryIntent(t *testing.T, s *Store, key string) (versioningrelease.Job, versioningrelease.Intent) {
	t.Helper()
	_, revision, err := s.Create(context.Background(), "tag", tagDraft("recovery"))
	if err != nil {
		t.Fatal(err)
	}
	policyID := mustID(t)
	if _, err = s.db.Exec(`INSERT INTO release_policies(id,display_version,canonical_body,canonical_hash,created_at) VALUES(?,?,?,?,?)`, policyID, 100, `{}`, strings.Repeat("a", 64), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	job, _, err := s.CreateOrGetReleaseJob(context.Background(), versioningrelease.JobRequest{ProjectID: s.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: key, RequestHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"gates":[]}`)
	now := time.Now().UTC()
	intent := versioningrelease.Intent{ID: mustID(t), JobID: job.ID, CandidateRevisionID: revision.ID, PolicyID: policyID, GateManifest: manifest, GateManifestHash: versioning.SHA256(manifest), Backup: versioningrelease.BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("c", 64)}, RequestHash: job.RequestHash, IdempotencyKey: job.IdempotencyKey, Phase: versioningrelease.IntentRecorded, CreatedAt: now, UpdatedAt: now}
	if _, existed, err := s.CreateIntent(context.Background(), intent); err != nil || existed {
		t.Fatalf("intent exists=%v err=%v", existed, err)
	}
	if _, swapped, err := s.TransitionReleaseJob(context.Background(), job.ID, versioningrelease.JobQueued, versioningrelease.JobRunning, nil); err != nil || !swapped {
		t.Fatalf("job running swapped=%v err=%v", swapped, err)
	}
	if _, swapped, err := s.TransitionIntent(context.Background(), intent.ID, versioningrelease.IntentRecorded, versioningrelease.IntentGraphActivating, "task", ""); err != nil || !swapped {
		t.Fatalf("graph activating swapped=%v err=%v", swapped, err)
	}
	if _, swapped, err := s.TransitionIntent(context.Background(), intent.ID, versioningrelease.IntentGraphActivating, versioningrelease.IntentGraphActivated, "task", ""); err != nil || !swapped {
		t.Fatalf("graph activated swapped=%v err=%v", swapped, err)
	}
	return job, intent
}

func assertNoCommittedRelease(t *testing.T, s *Store, intentID domain.ID) {
	t.Helper()
	pointer, err := s.GetActivePointer(context.Background())
	if err != nil || pointer.ReleaseID.Valid() || pointer.Generation != 0 {
		t.Fatalf("pointer=%#v err=%v", pointer, err)
	}
	var releases int
	if err := s.db.QueryRow(`SELECT count(*) FROM releases WHERE intent_id=?`, intentID).Scan(&releases); err != nil || releases != 0 {
		t.Fatalf("releases=%d err=%v", releases, err)
	}
	intent, err := s.GetIntent(context.Background(), intentID)
	if err != nil || intent.Phase != versioningrelease.IntentGraphActivated {
		t.Fatalf("intent=%#v err=%v", intent, err)
	}
}

type recoveryGraphFake struct {
	snapshot    versioningrelease.GraphSnapshot
	activations []versioningrelease.GraphActivationRequest
	reads       []versioningrelease.GraphReadRequest
	restores    []versioningrelease.GraphRestoreRequest
}

func (f *recoveryGraphFake) Activate(_ context.Context, request versioningrelease.GraphActivationRequest) (versioningrelease.GraphActivationEvidence, error) {
	f.activations = append(f.activations, request)
	f.snapshot = versioningrelease.GraphSnapshot{ProjectID: request.ProjectID, RevisionID: request.RevisionID, ConfigHash: request.ConfigHash}
	return versioningrelease.GraphActivationEvidence{Changed: true, ProjectID: request.ProjectID, RevisionID: request.RevisionID, ConfigHash: request.ConfigHash, IntentID: request.IntentID, TaskID: "graph-task"}, nil
}

func (f *recoveryGraphFake) ActiveSnapshot(_ context.Context, request versioningrelease.GraphReadRequest) (versioningrelease.GraphSnapshot, error) {
	f.reads = append(f.reads, request)
	return f.snapshot, nil
}

func (f *recoveryGraphFake) Restore(_ context.Context, request versioningrelease.GraphRestoreRequest) error {
	f.restores = append(f.restores, request)
	return nil
}
