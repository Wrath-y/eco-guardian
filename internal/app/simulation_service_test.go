package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

type simulationJobsFake struct{ record sharedjob.Record }

func (f *simulationJobsFake) CreateOrGet(_ context.Context, request sharedjob.Request) (sharedjob.Record, bool, error) {
	f.record = sharedjob.Record{ID: mustSimulationID(), ProjectID: request.ProjectID, Kind: request.Kind, RevisionID: request.RevisionID, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, Status: sharedjob.Queued, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	return f.record, false, nil
}
func (f *simulationJobsFake) GetJob(context.Context, domain.ID) (sharedjob.Record, error) {
	return f.record, nil
}
func (f *simulationJobsFake) Transition(context.Context, domain.ID, sharedjob.Status, sharedjob.Status, *sharedjob.Result, int64) (sharedjob.Record, bool, error) {
	return f.record, false, nil
}
func (f *simulationJobsFake) RequestCancellation(context.Context, domain.ID) (sharedjob.Record, bool, error) {
	return f.record, false, nil
}

type simulationMaterializationsFake struct{ value contract.JobMaterialization }

func (f *simulationMaterializationsFake) SaveSimulationJobMaterialization(_ context.Context, value contract.JobMaterialization) error {
	f.value = value
	return nil
}
func mustSimulationID() domain.ID { id, _ := domain.NewID(); return id }

func TestSimulationApplicationPersistsCapturedInputWithJob(t *testing.T) {
	project, revision, sceneID := mustSimulationID(), mustSimulationID(), mustSimulationID()
	input, err := contract.NormalizeInput(contract.InputRequest{Revision: contract.Revision{ID: contract.ID(revision), ProjectID: contract.ID(project), ConfigHash: strings.Repeat("a", 64), ManifestHash: strings.Repeat("b", 64)}, Scene: scenario.BuiltinTemplates()[0], Metrics: []contract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	jobs, materializations := &simulationJobsFake{}, &simulationMaterializationsFake{}
	verifyRunID := mustSimulationID()
	job, replay, err := (SimulationApplication{Jobs: jobs, Materializations: materializations}).SubmitSimulation(context.Background(), SimulationSubmission{Input: input, ScenarioDefinitionID: sceneID, VerifyRunID: verifyRunID, FingerprintHash: strings.Repeat("c", 64), IdempotencyKey: "simulation"})
	if err != nil || replay || materializations.value.JobID != job.ID || materializations.value.InputHash != job.InputHash || materializations.value.VerifySourceRunID != verifyRunID {
		t.Fatalf("job=%#v materialization=%#v err=%v", job, materializations.value, err)
	}
}
