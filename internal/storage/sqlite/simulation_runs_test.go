package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
)

func TestSimulationRunAndMetricFactsAreInsertOnlyWhileCheckpointIsRecoverable(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("simulationrun"))
	if err != nil {
		t.Fatal(err)
	}
	scene, err := store.GetScenarioDefinition(context.Background(), "single-target-30s", "v1")
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := store.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: store.ProjectID(), Kind: "simulation", RevisionID: revision.ID, InputHash: strings.Repeat("a", 64), IdempotencyKey: "simulation-run", RequestHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	run := SimulationRun{ID: mustID(t), JobID: job.ID, ProjectID: store.ProjectID(), RevisionID: revision.ID, ScenarioDefinitionID: scene.ID, InputHash: strings.Repeat("a", 64), FingerprintHash: strings.Repeat("c", 64), ResultHash: strings.Repeat("d", 64), CanonicalResult: `{"schema_version":"v1"}`, CreatedAt: time.Now().UTC(), Implementations: contract.V1Descriptors()}
	if err = store.InsertSimulationRun(context.Background(), run, []SimulationMetricResult{{MetricID: "metric-dps", MetricVersion: "v1", Status: "available", CanonicalResult: `{"value":"1"}`}}); err != nil {
		t.Fatal(err)
	}
	loaded, metrics, err := store.GetSimulationRun(context.Background(), run.ID)
	if err != nil || loaded.ResultHash != run.ResultHash || len(metrics) != 1 || len(loaded.Implementations) != len(contract.RequiredV1Descriptors) {
		t.Fatalf("run=%#v metrics=%#v err=%v", loaded, metrics, err)
	}
	if _, err = store.db.Exec(`UPDATE simulation_run_implementations SET descriptor_hash=? WHERE run_id=? AND descriptor_id='simulation-engine'`, strings.Repeat("e", 64), run.ID); err == nil {
		t.Fatal("expected immutable implementation history")
	}
	reconciled, replay, err := store.ReconcileSealedSimulationRun(context.Background(), job.ID, run.InputHash, run.ResultHash)
	if err != nil || replay || reconciled.Status != sharedjob.Succeeded || reconciled.Result == nil || reconciled.Result.ID != run.ID {
		t.Fatalf("reconciled=%#v replay=%v err=%v", reconciled, replay, err)
	}
	if _, replay, err = store.ReconcileSealedSimulationRun(context.Background(), job.ID, run.InputHash, run.ResultHash); err != nil || !replay {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	if _, _, err = store.ReconcileSealedSimulationRun(context.Background(), job.ID, run.InputHash, strings.Repeat("e", 64)); !errors.Is(err, ErrSimulationReplay) {
		t.Fatalf("expected replay mismatch, got %v", err)
	}
	if _, err = store.db.Exec(`UPDATE simulation_runs SET result_hash=? WHERE id=?`, strings.Repeat("e", 64), run.ID); err == nil {
		t.Fatal("expected immutable run")
	}
	secondJob, _, err := store.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: store.ProjectID(), Kind: "simulation", RevisionID: revision.ID, InputHash: run.InputHash, IdempotencyKey: "simulation-verify", RequestHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	secondRun := run
	secondRun.ID, secondRun.JobID = mustID(t), secondJob.ID
	if err = store.InsertSimulationRun(context.Background(), secondRun, []SimulationMetricResult{{MetricID: "metric-dps", MetricVersion: "v1", Status: "available", CanonicalResult: `{"value":"1"}`}}); err != nil {
		t.Fatal(err)
	}
	verification, err := store.VerifySimulationRuns(context.Background(), run.ID, secondRun.ID)
	if err != nil || verification.Status != "verified" || verification.SourceResultHash != run.ResultHash {
		t.Fatalf("verification=%#v err=%v", verification, err)
	}
	links, err := store.ListSimulationVerifications(context.Background(), run.ID)
	if err != nil || len(links) != 1 || links[0].ID != verification.ID || links[0].ReproductionRunID != secondRun.ID {
		t.Fatalf("verification links=%#v err=%v", links, err)
	}
	checkpoint := SimulationCheckpoint{JobID: job.ID, SampleOrdinal: 0, InputHash: run.InputHash, FingerprintHash: run.FingerprintHash, CancelGeneration: 0, Accumulator: `{"sample":0}`, AccumulatorHash: SimulationAccumulatorHash(`{"sample":0}`), CompletedAt: time.Now().UTC()}
	if err = store.SaveSimulationCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint.Accumulator = `{"sample":0,"replayed":true}`
	checkpoint.AccumulatorHash = SimulationAccumulatorHash(checkpoint.Accumulator)
	if err = store.SaveSimulationCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	loadedCheckpoints, err := store.ListSimulationCheckpoints(context.Background(), job.ID, run.InputHash, run.FingerprintHash, 0)
	if err != nil || len(loadedCheckpoints) != 1 || loadedCheckpoints[0].Accumulator != checkpoint.Accumulator {
		t.Fatalf("checkpoints=%#v err=%v", loadedCheckpoints, err)
	}
	checkpoint.InputHash = strings.Repeat("9", 64)
	if err = store.SaveSimulationCheckpoint(context.Background(), checkpoint); err == nil {
		t.Fatal("expected checkpoint identity mismatch")
	}
}

func TestSimulationJobMaterializationIsImmutableAndBoundToJobIdentity(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("simulationmaterialization"))
	if err != nil {
		t.Fatal(err)
	}
	scene, err := store.GetScenarioDefinition(context.Background(), "single-target-30s", "v1")
	if err != nil {
		t.Fatal(err)
	}
	canonical := []byte("eco-guardian/simulation-input/v1\x00{\"captured\":true}")
	inputHash := hashSimulationBytes(canonical)
	job, _, err := store.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: store.ProjectID(), Kind: "simulation", RevisionID: revision.ID, InputHash: inputHash, IdempotencyKey: "simulation-materialization", RequestHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	materialization := SimulationJobMaterialization{JobID: job.ID, ProjectID: store.ProjectID(), RevisionID: revision.ID, ScenarioDefinitionID: scene.ID, CanonicalInput: canonical, InputHash: inputHash, FingerprintHash: strings.Repeat("c", 64), CreatedAt: time.Now().UTC()}
	if err = store.SaveSimulationJobMaterialization(context.Background(), materialization); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetSimulationJobMaterialization(context.Background(), job.ID)
	if err != nil || string(loaded.CanonicalInput) != string(canonical) || loaded.FingerprintHash != materialization.FingerprintHash {
		t.Fatalf("materialization=%#v err=%v", loaded, err)
	}
	if err = store.SaveSimulationJobMaterialization(context.Background(), materialization); err == nil {
		t.Fatal("expected duplicate immutable materialization rejection")
	}
	if _, err = store.db.Exec(`UPDATE simulation_job_materializations SET fingerprint_hash=? WHERE job_id=?`, strings.Repeat("d", 64), job.ID); err == nil {
		t.Fatal("expected immutable materialization")
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := Open(filepath.Dir(store.path), store.registry)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recoverable, err := reopened.ListRecoverableSimulationJobs(context.Background())
	if err != nil || len(recoverable) != 1 || recoverable[0].ID != job.ID {
		t.Fatalf("recoverable=%#v err=%v", recoverable, err)
	}
	if _, err = reopened.GetSimulationJobMaterialization(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSimulationSealIsAtomicAndRejectsNewCancellationGeneration(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("simulationseal"))
	if err != nil {
		t.Fatal(err)
	}
	scene, err := store.GetScenarioDefinition(context.Background(), "single-target-30s", "v1")
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := store.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: store.ProjectID(), Kind: "simulation", RevisionID: revision.ID, InputHash: strings.Repeat("a", 64), IdempotencyKey: "simulation-seal", RequestHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := store.Transition(context.Background(), job.ID, sharedjob.Queued, sharedjob.Running, nil, 0); err != nil || !changed {
		t.Fatalf("start changed=%v err=%v", changed, err)
	}
	run := SimulationRun{ID: mustID(t), JobID: job.ID, ProjectID: store.ProjectID(), RevisionID: revision.ID, ScenarioDefinitionID: scene.ID, InputHash: strings.Repeat("a", 64), FingerprintHash: strings.Repeat("c", 64), ResultHash: strings.Repeat("d", 64), CanonicalResult: `{"schema_version":"v1"}`, CreatedAt: time.Now().UTC(), Implementations: contract.V1Descriptors()}
	metrics := []SimulationMetricResult{{MetricID: "metric-dps", MetricVersion: "v1", Status: "available", CanonicalResult: `{"value":"1"}`}}
	if err = store.SealSimulationRun(context.Background(), run, metrics, 1, 0); err == nil {
		t.Fatal("expected incomplete sample seal rejection")
	}
	checkpoint := SimulationCheckpoint{JobID: job.ID, SampleOrdinal: 0, InputHash: run.InputHash, FingerprintHash: run.FingerprintHash, CancelGeneration: 0, Accumulator: `{"sample":0}`, AccumulatorHash: SimulationAccumulatorHash(`{"sample":0}`), CompletedAt: time.Now().UTC()}
	if err = store.SaveSimulationCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	if err = store.SealSimulationRun(context.Background(), run, metrics, 1, 0); err != nil {
		t.Fatal(err)
	}
	sealed, err := store.GetJob(context.Background(), job.ID)
	if err != nil || sealed.Status != sharedjob.Succeeded || sealed.Result == nil || sealed.Result.ID != run.ID {
		t.Fatalf("sealed=%#v err=%v", sealed, err)
	}
	if _, _, err = store.GetSimulationRun(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	second, _, err := store.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: store.ProjectID(), Kind: "simulation", RevisionID: revision.ID, InputHash: strings.Repeat("e", 64), IdempotencyKey: "simulation-canceled", RequestHash: strings.Repeat("f", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Transition(context.Background(), second.ID, sharedjob.Queued, sharedjob.Running, nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.RequestCancellation(context.Background(), second.ID); err != nil {
		t.Fatal(err)
	}
	run.ID, run.JobID, run.InputHash = mustID(t), second.ID, strings.Repeat("e", 64)
	if err = store.SealSimulationRun(context.Background(), run, metrics, 1, 0); err == nil {
		t.Fatal("expected cancellation generation seal rejection")
	}
	if _, _, err = store.GetSimulationRun(context.Background(), run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled run visible err=%v", err)
	}
}

func TestSimulationFailureAtomicallyRetainsDiagnosticWithoutRun(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("simulationfailure"))
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := store.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: store.ProjectID(), Kind: "simulation", RevisionID: revision.ID, InputHash: strings.Repeat("a", 64), IdempotencyKey: "simulation-failure", RequestHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, changed, err := store.Transition(context.Background(), job.ID, sharedjob.Queued, sharedjob.Running, nil, 0); err != nil || !changed {
		t.Fatalf("start changed=%v err=%v", changed, err)
	}
	failed, replay, err := store.FailSimulationJob(context.Background(), job.ID, 0, "BUDGET_EXCEEDED", "event budget exceeded")
	if err != nil || replay || failed.Status != sharedjob.Failed {
		t.Fatalf("failed=%#v replay=%v err=%v", failed, replay, err)
	}
	events, err := store.ListEvents(context.Background(), job.ID, 0)
	if err != nil || len(events) != 1 || events[0].Phase != "FAILED" || events[0].SafeError != "BUDGET_EXCEEDED: event budget exceeded" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	if _, replay, err = store.FailSimulationJob(context.Background(), job.ID, 0, "BUDGET_EXCEEDED", "event budget exceeded"); err != nil || !replay {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	if _, _, err = store.FailSimulationJob(context.Background(), job.ID, 0, "TIMEOUT", "runtime deadline exceeded"); !errors.Is(err, ErrSimulationFailure) {
		t.Fatalf("expected mismatched failure rejection, got %v", err)
	}
	if _, _, err = store.GetSimulationRun(context.Background(), job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failure created visible run err=%v", err)
	}
}

func TestSimulationCheckpointFaultDoesNotExposePartialState(t *testing.T) {
	for _, stage := range []string{"simulation-checkpoint-before-write", "simulation-checkpoint-after-write"} {
		t.Run(stage, func(t *testing.T) {
			store := newStore(t)
			jobID := mustID(t)
			store.failStage = func(at string) error {
				if at == stage {
					return errors.New("injected")
				}
				return nil
			}
			err := store.SaveSimulationCheckpoint(context.Background(), SimulationCheckpoint{JobID: jobID, SampleOrdinal: 0, InputHash: strings.Repeat("a", 64), FingerprintHash: strings.Repeat("b", 64), Accumulator: `{"sample":0}`, AccumulatorHash: SimulationAccumulatorHash(`{"sample":0}`), CompletedAt: time.Now().UTC()})
			if err == nil {
				t.Fatal("expected injected checkpoint failure")
			}
			if rows, queryErr := store.ListSimulationCheckpoints(context.Background(), jobID, strings.Repeat("a", 64), strings.Repeat("b", 64), 0); queryErr != nil || len(rows) != 0 {
				t.Fatalf("rows=%#v err=%v", rows, queryErr)
			}
		})
	}
}

func TestSimulationCancellationFaultsRollbackTheEntireIntent(t *testing.T) {
	for _, stage := range []string{"shared-job-cancel-before-write", "shared-job-cancel-after-write"} {
		t.Run(stage, func(t *testing.T) {
			store := newStore(t)
			_, revision, err := store.Create(context.Background(), "tag", tagDraft("simulationcancelfault"))
			if err != nil {
				t.Fatal(err)
			}
			job, _, err := store.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: store.ProjectID(), Kind: "simulation", RevisionID: revision.ID, InputHash: strings.Repeat("a", 64), IdempotencyKey: stage, RequestHash: strings.Repeat("b", 64)})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = store.Transition(context.Background(), job.ID, sharedjob.Queued, sharedjob.Running, nil, 0); err != nil {
				t.Fatal(err)
			}
			store.failStage = func(at string) error {
				if at == stage {
					return errors.New("injected")
				}
				return nil
			}
			if _, _, err = store.RequestCancellation(context.Background(), job.ID); err == nil {
				t.Fatal("expected cancellation fault")
			}
			current, err := store.GetJob(context.Background(), job.ID)
			if err != nil || current.Status != sharedjob.Running || current.CancelGeneration != 0 || current.CancelRequestedAt != nil {
				t.Fatalf("job=%#v err=%v", current, err)
			}
		})
	}
}

func TestSimulationSealFaultsDoNotExposeRunOrJobSuccess(t *testing.T) {
	for _, stage := range []string{"simulation-seal-before-run", "simulation-seal-after-run"} {
		t.Run(stage, func(t *testing.T) {
			store := newStore(t)
			_, revision, err := store.Create(context.Background(), "tag", tagDraft("simulationsealfault"))
			if err != nil {
				t.Fatal(err)
			}
			scene, err := store.GetScenarioDefinition(context.Background(), "single-target-30s", "v1")
			if err != nil {
				t.Fatal(err)
			}
			job, _, err := store.CreateOrGet(context.Background(), sharedjob.Request{ProjectID: store.ProjectID(), Kind: "simulation", RevisionID: revision.ID, InputHash: strings.Repeat("a", 64), IdempotencyKey: stage, RequestHash: strings.Repeat("b", 64)})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err = store.Transition(context.Background(), job.ID, sharedjob.Queued, sharedjob.Running, nil, 0); err != nil {
				t.Fatal(err)
			}
			run := SimulationRun{ID: mustID(t), JobID: job.ID, ProjectID: store.ProjectID(), RevisionID: revision.ID, ScenarioDefinitionID: scene.ID, InputHash: strings.Repeat("a", 64), FingerprintHash: strings.Repeat("c", 64), ResultHash: strings.Repeat("d", 64), CanonicalResult: `{"schema_version":"v1"}`, CreatedAt: time.Now().UTC(), Implementations: contract.V1Descriptors()}
			if err = store.SaveSimulationCheckpoint(context.Background(), SimulationCheckpoint{JobID: job.ID, InputHash: run.InputHash, FingerprintHash: run.FingerprintHash, Accumulator: `{"sample":0}`, AccumulatorHash: SimulationAccumulatorHash(`{"sample":0}`), CompletedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			store.failStage = func(at string) error {
				if at == stage {
					return errors.New("injected")
				}
				return nil
			}
			if err = store.SealSimulationRun(context.Background(), run, []SimulationMetricResult{{MetricID: "metric-dps", MetricVersion: "v1", Status: "available", CanonicalResult: `{"value":"1"}`}}, 1, 0); err == nil {
				t.Fatal("expected seal fault")
			}
			if _, _, err = store.GetSimulationRun(context.Background(), run.ID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("run visible: %v", err)
			}
			current, err := store.GetJob(context.Background(), job.ID)
			if err != nil || current.Status != sharedjob.Running || current.Result != nil {
				t.Fatalf("job=%#v err=%v", current, err)
			}
		})
	}
}
