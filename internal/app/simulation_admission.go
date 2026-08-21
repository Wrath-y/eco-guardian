package app

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/orchestration"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

var (
	ErrSimulationAdmissionUnavailable      = errors.New("simulation admission is unavailable")
	ErrSimulationSceneInvalid              = errors.New("simulation scene is invalid")
	ErrSimulationParameterInvalid          = errors.New("simulation parameter is invalid")
	ErrSimulationMetricInvalid             = errors.New("simulation metric is invalid")
	ErrSimulationSampleInvalid             = errors.New("simulation sample count is invalid")
	ErrSimulationBudgetInvalid             = errors.New("simulation budget is invalid")
	ErrSimulationImplementationUnavailable = errors.New("simulation implementation is unavailable")
	ErrSimulationVerificationTargetInvalid = errors.New("simulation verification target is invalid")
)

type SimulationAdmissionService interface {
	AdmitSimulation(context.Context, SimulationAdmission) (SimulationAdmissionResult, error)
}

type SimulationAdmission struct {
	ProjectID      domain.ID
	RevisionID     domain.ID
	ReleaseID      domain.ID
	SceneID        string
	SceneVersion   string
	Metrics        []contract.MetricIdentity
	SampleCount    int
	Seed           *uint64
	Parameters     []scenario.ParameterOverlay
	Budget         *contract.BudgetOverride
	VerifyRunID    domain.ID
	IdempotencyKey string
}

type SimulationAdmissionResult struct {
	Job      domain.ID
	Replayed bool
}

type simulationFingerprintResolver interface {
	ResolveSimulationFingerprint(context.Context, contract.SimulationInputV1) (string, error)
}

// SimulationAdmissionApplication owns only the sequencing of existing
// immutable ports. It does not read working state or create a second Job model.
type SimulationAdmissionApplication struct {
	Revisions     contract.RevisionSource
	Releases      contract.ReleaseSource
	Gate          contract.ValidationGate
	Scenarios     contract.ScenarioSource
	Fingerprints  simulationFingerprintResolver
	Verifications contract.VerificationSourceReader
	Jobs          SimulationService
}

func (s SimulationAdmissionApplication) AdmitSimulation(ctx context.Context, request SimulationAdmission) (SimulationAdmissionResult, error) {
	if !request.ProjectID.Valid() || request.IdempotencyKey == "" || s.Revisions == nil || s.Gate == nil || s.Scenarios == nil || s.Fingerprints == nil || s.Jobs == nil || (request.VerifyRunID.Valid() && s.Verifications == nil) {
		return SimulationAdmissionResult{}, ErrSimulationAdmissionUnavailable
	}
	if !request.RevisionID.Valid() == !request.ReleaseID.Valid() {
		return SimulationAdmissionResult{}, contract.ErrSourceInvalid
	}
	if request.SceneID == "" || request.SceneVersion == "" {
		return SimulationAdmissionResult{}, ErrSimulationSceneInvalid
	}
	if len(request.Metrics) == 0 {
		return SimulationAdmissionResult{}, ErrSimulationMetricInvalid
	}
	revision, err := orchestration.Admit(ctx, s.Revisions, s.Releases, s.Gate, contract.SourceSelection{ProjectID: contract.ID(request.ProjectID), RevisionID: contract.ID(request.RevisionID), ReleaseID: contract.ID(request.ReleaseID)})
	if err != nil {
		return SimulationAdmissionResult{}, err
	}
	scene, err := s.Scenarios.ResolveSimulationScenario(ctx, request.SceneID, request.SceneVersion)
	if err != nil || !scene.DefinitionID.Valid() {
		return SimulationAdmissionResult{}, ErrSimulationSceneInvalid
	}
	if request.SampleCount < 0 || request.SampleCount > scene.Template.Definition.Budgets.MaxSamples {
		return SimulationAdmissionResult{}, ErrSimulationSampleInvalid
	}
	if !validSimulationBudget(request.Budget, scene.Template.Definition.Budgets) {
		return SimulationAdmissionResult{}, ErrSimulationBudgetInvalid
	}
	template := scene.Template
	if len(request.Parameters) > 0 {
		template, err = scenario.Clone(scene.Template, scene.Template.Definition.ID, scene.Template.Definition.Version, request.Parameters, nil)
		if err != nil {
			return SimulationAdmissionResult{}, ErrSimulationParameterInvalid
		}
	}
	input, err := contract.NormalizeInput(contract.InputRequest{Revision: revision, Scene: template, SampleCount: request.SampleCount, Seed: request.Seed, Budget: request.Budget, Metrics: request.Metrics})
	if err != nil {
		return SimulationAdmissionResult{}, err
	}
	fingerprint, err := s.Fingerprints.ResolveSimulationFingerprint(ctx, input)
	if err != nil || fingerprint == "" {
		return SimulationAdmissionResult{}, ErrSimulationImplementationUnavailable
	}
	if request.VerifyRunID.Valid() {
		source, sourceErr := s.Verifications.ResolveSimulationVerificationSource(ctx, request.VerifyRunID)
		if sourceErr != nil || source.ProjectID != request.ProjectID || source.RevisionID != domain.ID(input.RevisionID) || source.ScenarioDefinitionID != scene.DefinitionID {
			return SimulationAdmissionResult{}, ErrSimulationVerificationTargetInvalid
		}
		inputHash, hashErr := input.Hash()
		if hashErr != nil || inputHash != source.InputHash || fingerprint != source.FingerprintHash {
			return SimulationAdmissionResult{}, ErrSimulationVerificationTargetInvalid
		}
	}
	job, replayed, err := s.Jobs.SubmitSimulation(ctx, SimulationSubmission{Input: input, ScenarioDefinitionID: scene.DefinitionID, VerifyRunID: request.VerifyRunID, FingerprintHash: fingerprint, IdempotencyKey: request.IdempotencyKey})
	if err != nil {
		return SimulationAdmissionResult{}, err
	}
	return SimulationAdmissionResult{Job: job.ID, Replayed: replayed}, nil
}

func validSimulationBudget(override *contract.BudgetOverride, limits scenario.Budgets) bool {
	if override == nil {
		return true
	}
	for _, budget := range []struct {
		value *int
		limit int
	}{{override.MaxEvents, limits.MaxEvents}, {override.MaxSteps, limits.MaxSteps}, {override.MaxRuntimeMS, limits.MaxRuntimeMS}} {
		if budget.value != nil && (*budget.value < 1 || *budget.value > budget.limit) {
			return false
		}
	}
	return true
}

var _ SimulationAdmissionService = SimulationAdmissionApplication{}
