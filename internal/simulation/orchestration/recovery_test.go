package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
)

type fingerprintResolverFake struct {
	fingerprint string
	err         error
}

type batchRecoveryStoreFake struct {
	jobs     []sharedjob.Record
	requests map[domain.ID]RecoveryRequest
	err      error
}

func (store batchRecoveryStoreFake) ListRecoverableSimulationJobs(context.Context) ([]sharedjob.Record, error) {
	return append([]sharedjob.Record(nil), store.jobs...), nil
}
func (store batchRecoveryStoreFake) LoadSimulationRecoveryRequest(_ context.Context, job sharedjob.Record) (RecoveryRequest, error) {
	if store.err != nil {
		return RecoveryRequest{}, store.err
	}
	return store.requests[job.ID], nil
}

func (fake fingerprintResolverFake) ResolveSimulationFingerprint(context.Context, []byte) (string, error) {
	return fake.fingerprint, fake.err
}

func recoveryRequest(t *testing.T) (RecoveryRequest, fingerprintResolverFake) {
	t.Helper()
	projectID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	revisionID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	sceneID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	canonical := []byte("eco-guardian/simulation-input/v1\x00{\"captured\":true}")
	digest := sha256.Sum256(canonical)
	inputHash := hex.EncodeToString(digest[:])
	fingerprint := strings.Repeat("b", 64)
	now := time.Now().UTC()
	job := sharedjob.Record{ID: jobID, ProjectID: projectID, Kind: "simulation", RevisionID: revisionID, InputHash: inputHash, IdempotencyKey: "recovery", RequestHash: strings.Repeat("a", 64), Status: sharedjob.Interrupted, CreatedAt: now, UpdatedAt: now}
	materialization := RecoveryMaterialization{JobID: jobID, ProjectID: projectID, RevisionID: revisionID, ScenarioDefinitionID: sceneID, CanonicalInput: canonical, InputHash: inputHash, FingerprintHash: fingerprint}
	return RecoveryRequest{Job: job, Materialization: materialization, SampleCount: 3, Checkpoints: []RecoveryCheckpoint{{Ordinal: 1, InputHash: inputHash, FingerprintHash: fingerprint, Accumulator: `{"sample":1}`, AccumulatorHash: contract.CheckpointAccumulatorHash(`{"sample":1}`)}}}, fingerprintResolverFake{fingerprint: fingerprint}
}

func TestPlanRecoveryReusesOnlyCompleteMatchingOrdinals(t *testing.T) {
	request, resolver := recoveryRequest(t)
	plan, err := PlanRecovery(context.Background(), resolver, request)
	if err != nil || len(plan.ReusableOrdinals) != 1 || plan.ReusableOrdinals[0] != 1 || len(plan.MissingOrdinals) != 2 || plan.MissingOrdinals[0] != 0 || plan.MissingOrdinals[1] != 2 {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
}

func TestRecoverRecomputesOnlyMissingSamplesUnderOriginalJob(t *testing.T) {
	request, resolver := recoveryRequest(t)
	called := []uint64{}
	plan, err := Recover(context.Background(), resolver, request, func(_ context.Context, job sharedjob.Record, materialization RecoveryMaterialization, ordinal uint64) error {
		if job.ID != request.Job.ID || materialization.InputHash != request.Materialization.InputHash {
			t.Fatal("recovery lost original immutable identity")
		}
		called = append(called, ordinal)
		return nil
	})
	if err != nil || len(plan.ReusableOrdinals) != 1 || len(called) != 2 || called[0] != 0 || called[1] != 2 {
		t.Fatalf("plan=%#v called=%v err=%v", plan, called, err)
	}
}

func TestPlanRecoveryRejectsImplementationDriftAndCorruptCheckpoint(t *testing.T) {
	request, resolver := recoveryRequest(t)
	resolver.fingerprint = strings.Repeat("c", 64)
	if _, err := PlanRecovery(context.Background(), resolver, request); !errors.Is(err, ErrRecoveryUnavailable) {
		t.Fatalf("expected unavailable implementation, got %v", err)
	}
	request, resolver = recoveryRequest(t)
	request.Checkpoints[0].AccumulatorHash = strings.Repeat("d", 64)
	if _, err := PlanRecovery(context.Background(), resolver, request); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("expected corrupt checkpoint, got %v", err)
	}
}

func TestRecoveryManagerPreservesOriginalJobAndPersistsFingerprintRefusal(t *testing.T) {
	request, resolver := recoveryRequest(t)
	store := batchRecoveryStoreFake{jobs: []sharedjob.Record{request.Job}, requests: map[domain.ID]RecoveryRequest{request.Job.ID: request}}
	failures := &failureStoreFake{record: request.Job}
	runs := 0
	manager := RecoveryManager{Requests: store, Resolver: resolver, Failures: failures, Runner: func(_ context.Context, job sharedjob.Record, _ RecoveryMaterialization, _ uint64) error {
		if job.ID != request.Job.ID {
			t.Fatal("recovery changed Job identity")
		}
		runs++
		return nil
	}}
	results, err := manager.RecoverAll(context.Background())
	if err != nil || len(results) != 1 || results[0].RecoveryRequired || results[0].ReusableSamples != 1 || results[0].RecomputedSamples != 2 || runs != 2 {
		t.Fatalf("results=%#v runs=%d err=%v", results, runs, err)
	}

	resolver.fingerprint = strings.Repeat("f", 64)
	manager.Resolver = resolver
	results, err = manager.RecoverAll(context.Background())
	if err != nil || !results[0].RecoveryRequired || results[0].Reason != FailureRecoveryUnavailable || failures.code != FailureRecoveryUnavailable || runs != 2 {
		t.Fatalf("drift results=%#v code=%s runs=%d err=%v", results, failures.code, runs, err)
	}
}
