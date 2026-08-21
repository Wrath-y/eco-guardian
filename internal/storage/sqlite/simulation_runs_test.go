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
	checkpoint := SimulationCheckpoint{JobID: job.ID, SampleOrdinal: 0, InputHash: run.InputHash, FingerprintHash: run.FingerprintHash, CancelGeneration: 0, Accumulator: `{"sample":0}`, AccumulatorHash: strings.Repeat("f", 64), CompletedAt: time.Now().UTC()}
	if err = store.SaveSimulationCheckpoint(context.Background(), checkpoint); err != nil {
		t.Fatal(err)
	}
	checkpoint.Accumulator = `{"sample":0,"replayed":true}`
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
	if err = store.SealSimulationRun(context.Background(), run, metrics, 0); err != nil {
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
	if err = store.SealSimulationRun(context.Background(), run, metrics, 0); err == nil {
		t.Fatal("expected cancellation generation seal rejection")
	}
	if _, _, err = store.GetSimulationRun(context.Background(), run.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("canceled run visible err=%v", err)
	}
}
