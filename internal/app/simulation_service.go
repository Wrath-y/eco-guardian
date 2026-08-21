package app

import (
	"context"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/orchestration"
)

var ErrSimulationOperationUnavailable = errors.New("simulation operation is unavailable")

type SimulationService interface {
	SubmitSimulation(context.Context, SimulationSubmission) (sharedjob.Record, bool, error)
}
type simulationMaterializationStore interface {
	SaveSimulationJobMaterialization(context.Context, SimulationMaterialization) error
}
type SimulationMaterialization struct {
	JobID, ProjectID, RevisionID, ScenarioDefinitionID domain.ID
	CanonicalInput                                     []byte
	InputHash, FingerprintHash                         string
	CreatedAt                                          time.Time
}
type SimulationSubmission struct {
	Input                           contract.SimulationInputV1
	ScenarioDefinitionID            domain.ID
	FingerprintHash, IdempotencyKey string
}
type SimulationApplication struct {
	Jobs             sharedjob.Store
	Materializations simulationMaterializationStore
	Clock            func() time.Time
}

func (s SimulationApplication) SubmitSimulation(ctx context.Context, submission SimulationSubmission) (sharedjob.Record, bool, error) {
	if s.Jobs == nil || s.Materializations == nil || !submission.ScenarioDefinitionID.Valid() || submission.FingerprintHash == "" || submission.IdempotencyKey == "" {
		return sharedjob.Record{}, false, ErrSimulationOperationUnavailable
	}
	canonical, err := submission.Input.CanonicalBytes()
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	inputHash, err := submission.Input.Hash()
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	job, replay, err := orchestration.CreateSimulationJob(ctx, s.Jobs, sharedjob.Request{ProjectID: domain.ID(submission.Input.ProjectID), Kind: "simulation", RevisionID: domain.ID(submission.Input.RevisionID), InputHash: inputHash, IdempotencyKey: submission.IdempotencyKey, RequestHash: inputHash})
	if err != nil || replay {
		return job, replay, err
	}
	now := time.Now().UTC()
	if s.Clock != nil {
		now = s.Clock().UTC()
	}
	materialization := SimulationMaterialization{JobID: job.ID, ProjectID: job.ProjectID, RevisionID: job.RevisionID, ScenarioDefinitionID: submission.ScenarioDefinitionID, CanonicalInput: canonical, InputHash: inputHash, FingerprintHash: submission.FingerprintHash, CreatedAt: now}
	if err = s.Materializations.SaveSimulationJobMaterialization(ctx, materialization); err != nil {
		return sharedjob.Record{}, false, err
	}
	return job, false, nil
}

var _ SimulationService = SimulationApplication{}
