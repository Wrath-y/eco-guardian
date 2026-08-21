package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sharedjob "github.com/zouyi/eco-guardian/internal/job"
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
	run := SimulationRun{ID: mustID(t), JobID: job.ID, ProjectID: store.ProjectID(), RevisionID: revision.ID, ScenarioDefinitionID: scene.ID, InputHash: strings.Repeat("a", 64), FingerprintHash: strings.Repeat("c", 64), ResultHash: strings.Repeat("d", 64), CanonicalResult: `{"schema_version":"v1"}`, CreatedAt: time.Now().UTC()}
	if err = store.InsertSimulationRun(context.Background(), run, []SimulationMetricResult{{MetricID: "metric-dps", MetricVersion: "v1", Status: "available", CanonicalResult: `{"value":"1"}`}}); err != nil {
		t.Fatal(err)
	}
	loaded, metrics, err := store.GetSimulationRun(context.Background(), run.ID)
	if err != nil || loaded.ResultHash != run.ResultHash || len(metrics) != 1 {
		t.Fatalf("run=%#v metrics=%#v err=%v", loaded, metrics, err)
	}
	if _, err = store.db.Exec(`UPDATE simulation_runs SET result_hash=? WHERE id=?`, strings.Repeat("e", 64), run.ID); err == nil {
		t.Fatal("expected immutable run")
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
	run := SimulationRun{ID: mustID(t), JobID: job.ID, ProjectID: store.ProjectID(), RevisionID: revision.ID, ScenarioDefinitionID: scene.ID, InputHash: strings.Repeat("a", 64), FingerprintHash: strings.Repeat("c", 64), ResultHash: strings.Repeat("d", 64), CanonicalResult: `{"schema_version":"v1"}`, CreatedAt: time.Now().UTC()}
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
