package app

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/orchestration"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

var ErrSimulationAdmissionUnavailable = errors.New("simulation admission is unavailable")

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
	if !request.ProjectID.Valid() || request.SceneID == "" || request.SceneVersion == "" || request.IdempotencyKey == "" || s.Revisions == nil || s.Gate == nil || s.Scenarios == nil || s.Fingerprints == nil || s.Jobs == nil || (request.VerifyRunID.Valid() && s.Verifications == nil) {
		return SimulationAdmissionResult{}, ErrSimulationAdmissionUnavailable
	}
	revision, err := orchestration.Admit(ctx, s.Revisions, s.Releases, s.Gate, contract.SourceSelection{ProjectID: contract.ID(request.ProjectID), RevisionID: contract.ID(request.RevisionID), ReleaseID: contract.ID(request.ReleaseID)})
	if err != nil {
		return SimulationAdmissionResult{}, err
	}
	scene, err := s.Scenarios.ResolveSimulationScenario(ctx, request.SceneID, request.SceneVersion)
	if err != nil || !scene.DefinitionID.Valid() {
		return SimulationAdmissionResult{}, ErrSimulationAdmissionUnavailable
	}
	template := scene.Template
	if len(request.Parameters) > 0 {
		template, err = scenario.Clone(scene.Template, scene.Template.Definition.ID, scene.Template.Definition.Version, request.Parameters, nil)
		if err != nil {
			return SimulationAdmissionResult{}, err
		}
	}
	input, err := contract.NormalizeInput(contract.InputRequest{Revision: revision, Scene: template, SampleCount: request.SampleCount, Seed: request.Seed, Budget: request.Budget, Metrics: request.Metrics})
	if err != nil {
		return SimulationAdmissionResult{}, err
	}
	fingerprint, err := s.Fingerprints.ResolveSimulationFingerprint(ctx, input)
	if err != nil || fingerprint == "" {
		return SimulationAdmissionResult{}, ErrSimulationAdmissionUnavailable
	}
	if request.VerifyRunID.Valid() {
		source, sourceErr := s.Verifications.ResolveSimulationVerificationSource(ctx, request.VerifyRunID)
		if sourceErr != nil || source.ProjectID != request.ProjectID || source.RevisionID != domain.ID(input.RevisionID) || source.ScenarioDefinitionID != scene.DefinitionID {
			return SimulationAdmissionResult{}, ErrSimulationAdmissionUnavailable
		}
		inputHash, hashErr := input.Hash()
		if hashErr != nil || inputHash != source.InputHash || fingerprint != source.FingerprintHash {
			return SimulationAdmissionResult{}, ErrSimulationAdmissionUnavailable
		}
	}
	job, replayed, err := s.Jobs.SubmitSimulation(ctx, SimulationSubmission{Input: input, ScenarioDefinitionID: scene.DefinitionID, VerifyRunID: request.VerifyRunID, FingerprintHash: fingerprint, IdempotencyKey: request.IdempotencyKey})
	if err != nil {
		return SimulationAdmissionResult{}, err
	}
	return SimulationAdmissionResult{Job: job.ID, Replayed: replayed}, nil
}

var _ SimulationAdmissionService = SimulationAdmissionApplication{}
