package release

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioning "github.com/zouyi/eco-guardian/internal/versioning"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func TestIntentRecoveryRestoresCandidateAndFailsWhenBaselineChanged(t *testing.T) {
	job := workerJob(t)
	job.Status = JobInterrupted
	now := time.Now().UTC()
	manifest := []byte(`{"gates":[]}`)
	intent := Intent{
		ID:                    workerID(t),
		JobID:                 job.ID,
		CandidateRevisionID:   job.RevisionID,
		BaselineReleaseID:     workerID(t),
		PolicyID:              workerID(t),
		GateManifest:          manifest,
		GateManifestHash:      versioning.SHA256(manifest),
		Backup:                BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("c", 64)},
		PreviousGraphIdentity: "graph-before-candidate",
		PreviousReleaseID:     workerID(t),
		RequestHash:           job.RequestHash,
		IdempotencyKey:        job.IdempotencyKey,
		Phase:                 IntentGraphActivated,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	store := &recoveryStore{intent: intent, job: job}
	activeRelease := Release{ID: workerID(t), RevisionID: workerID(t), PolicyID: workerID(t), IntentID: workerID(t), CreatedAt: now}
	graph := &recoveryGraph{snapshot: GraphSnapshot{ProjectID: job.ProjectID, RevisionID: job.RevisionID, ConfigHash: job.InputHash}}
	recovery := IntentRecovery{
		Intents: store,
		Jobs:    store,
		Sources: recoverySources{pointer: ActivePointer{ReleaseID: activeRelease.ID, Generation: 4}, release: activeRelease},
		Commit:  recoveryCommitter{t: t},
		Graph:   graph,
	}
	results, err := recovery.Recover(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != RecoveryFailed || !errors.Is(results[0].Err, ErrRecoveryRequired) {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	if store.intent.Phase != IntentFailed || store.job.Status != JobFailed {
		t.Fatalf("intent=%#v job=%#v", store.intent, store.job)
	}
	if len(graph.restores) != 1 || graph.restores[0].PreviousGraphIdentity != intent.PreviousGraphIdentity || graph.restores[0].PreviousReleaseID != intent.PreviousReleaseID || graph.restores[0].IntentID != intent.ID {
		t.Fatalf("restores=%#v", graph.restores)
	}
}

func TestIntentRecoveryContinuesWhenGraphAcceptedBeforeTransportFailure(t *testing.T) {
	job := workerJob(t)
	job.Status = JobInterrupted
	now := time.Now().UTC()
	manifest := []byte(`{"gates":[]}`)
	intent := Intent{ID: workerID(t), JobID: job.ID, CandidateRevisionID: job.RevisionID, PolicyID: workerID(t), GateManifest: manifest, GateManifestHash: versioning.SHA256(manifest), Backup: BackupEvidence{Online: true, IntegrityChecked: true, Checksum: strings.Repeat("c", 64)}, RequestHash: job.RequestHash, IdempotencyKey: job.IdempotencyKey, Phase: IntentGraphActivating, CreatedAt: now, UpdatedAt: now}
	store := &recoveryStore{intent: intent, job: job}
	release := Release{ID: workerID(t), RevisionID: job.RevisionID, PolicyID: intent.PolicyID, IntentID: intent.ID, CreatedAt: now}
	graph := &recoveryGraph{activateErr: errors.New("connection dropped after accept")}
	recovery := IntentRecovery{
		Intents: store,
		Jobs:    store,
		Sources: recoverySources{pointer: ActivePointer{}},
		Commit:  successfulRecoveryCommitter{store: store, release: release},
		Graph:   graph,
	}
	results, err := recovery.Recover(context.Background())
	if err != nil || len(results) != 1 || results[0].Outcome != RecoveryContinued {
		t.Fatalf("results=%#v err=%v", results, err)
	}
	if store.intent.Phase != IntentSucceeded || store.job.Status != JobSucceeded || len(graph.reads) != 2 {
		t.Fatalf("intent=%#v job=%#v reads=%#v", store.intent, store.job, graph.reads)
	}
}

type recoveryStore struct {
	intent Intent
	job    Job
}

func (s *recoveryStore) CreateIntent(context.Context, Intent) (Intent, bool, error) {
	return Intent{}, false, errors.New("unused")
}
func (s *recoveryStore) GetIntent(_ context.Context, id domain.ID) (Intent, error) {
	if id != s.intent.ID {
		return Intent{}, errors.New("missing intent")
	}
	return s.intent, nil
}
func (s *recoveryStore) ListNonterminalReleaseIntents(context.Context) ([]Intent, error) {
	return []Intent{s.intent}, nil
}
func (s *recoveryStore) TransitionIntent(_ context.Context, id domain.ID, expected, next IntentPhase, external, details string) (Intent, bool, error) {
	if id != s.intent.ID || s.intent.Phase != expected {
		return Intent{}, false, nil
	}
	s.intent.Phase, s.intent.ExternalTaskID, s.intent.ErrorDetails, s.intent.UpdatedAt = next, external, details, time.Now().UTC()
	return s.intent, true, nil
}
func (s *recoveryStore) GetReleaseJob(_ context.Context, id domain.ID) (Job, error) {
	if id != s.job.ID {
		return Job{}, errors.New("missing job")
	}
	return s.job, nil
}
func (s *recoveryStore) TransitionReleaseJob(_ context.Context, id domain.ID, expected, next JobStatus, result *JobResult) (Job, bool, error) {
	if id != s.job.ID || s.job.Status != expected || !expected.CanTransitionTo(next) {
		return Job{}, false, nil
	}
	s.job.Status, s.job.Result, s.job.UpdatedAt = next, result, time.Now().UTC()
	return s.job, true, nil
}

type recoverySources struct {
	pointer ActivePointer
	release Release
}

func (recoverySources) GetRevisionRecord(context.Context, domain.ID) (versioningrevision.Record, error) {
	return versioningrevision.Record{}, errors.New("unused")
}
func (recoverySources) GetPolicy(context.Context, domain.ID) (versioningpolicy.ReleasePolicy, error) {
	return versioningpolicy.ReleasePolicy{}, errors.New("unused")
}
func (s recoverySources) GetRelease(context.Context, domain.ID) (Release, error) {
	return s.release, nil
}
func (s recoverySources) GetActivePointer(context.Context) (ActivePointer, error) {
	return s.pointer, nil
}

type recoveryCommitter struct{ t *testing.T }

func (c recoveryCommitter) CommitActivatedIntent(context.Context, domain.ID, int64) (Release, ActivePointer, error) {
	c.t.Fatal("baseline conflict must not commit a release")
	return Release{}, ActivePointer{}, nil
}

type successfulRecoveryCommitter struct {
	store   *recoveryStore
	release Release
}

func (c successfulRecoveryCommitter) CommitActivatedIntent(_ context.Context, id domain.ID, _ int64) (Release, ActivePointer, error) {
	if id != c.store.intent.ID {
		return Release{}, ActivePointer{}, errors.New("wrong intent")
	}
	c.store.intent.Phase = IntentSucceeded
	return c.release, ActivePointer{ReleaseID: c.release.ID, Generation: 1}, nil
}

type recoveryGraph struct {
	snapshot    GraphSnapshot
	restores    []GraphRestoreRequest
	reads       []GraphReadRequest
	activateErr error
}

func (g *recoveryGraph) Activate(_ context.Context, request GraphActivationRequest) (GraphActivationEvidence, error) {
	g.snapshot = GraphSnapshot{ProjectID: request.ProjectID, RevisionID: request.RevisionID, ConfigHash: request.ConfigHash}
	return GraphActivationEvidence{ProjectID: request.ProjectID, RevisionID: request.RevisionID, ConfigHash: request.ConfigHash, IntentID: request.IntentID}, g.activateErr
}
func (g *recoveryGraph) ActiveSnapshot(_ context.Context, request GraphReadRequest) (GraphSnapshot, error) {
	if !request.Valid() {
		return GraphSnapshot{}, errors.New("invalid Graph read")
	}
	g.reads = append(g.reads, request)
	return g.snapshot, nil
}
func (g *recoveryGraph) Restore(_ context.Context, request GraphRestoreRequest) error {
	g.restores = append(g.restores, request)
	return nil
}
