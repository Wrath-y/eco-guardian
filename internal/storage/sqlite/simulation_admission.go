package sqlite

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
)

// ResolveRevision is the simulation adapter for one immutable revision. It is
// project-scoped by GetRevisionRecord and never reads working state.
func (s *Store) ResolveRevision(ctx context.Context, id contract.ID) (contract.Revision, error) {
	record, err := s.GetRevisionRecord(ctx, domain.ID(id))
	if err != nil {
		return contract.Revision{}, err
	}
	return contract.Revision{ID: contract.ID(record.Metadata.RevisionID), ProjectID: contract.ID(s.projectID), ConfigHash: record.Metadata.ConfigHash, ManifestHash: record.Metadata.ManifestHash}, nil
}

// ResolveReleaseRevision resolves the release's immutable pinned revision
// exactly once, then delegates to the same scoped revision materialization.
func (s *Store) ResolveReleaseRevision(ctx context.Context, id contract.ID) (contract.Revision, error) {
	release, err := s.GetRelease(ctx, domain.ID(id))
	if err != nil {
		return contract.Revision{}, err
	}
	return s.ResolveRevision(ctx, contract.ID(release.RevisionID))
}

func (s *Store) ResolveSimulationScenario(ctx context.Context, sceneID, version string) (contract.CapturedScenario, error) {
	definition, err := s.GetScenarioDefinition(ctx, sceneID, version)
	if err != nil {
		return contract.CapturedScenario{}, err
	}
	return contract.CapturedScenario{DefinitionID: definition.ID, Template: definition.Template}, nil
}

func (s *Store) ResolveSimulationVerificationSource(ctx context.Context, runID domain.ID) (contract.VerificationSource, error) {
	run, _, err := s.GetSimulationRun(ctx, runID)
	if err != nil {
		return contract.VerificationSource{}, err
	}
	materialization, err := s.GetSimulationJobMaterialization(ctx, run.JobID)
	if err != nil || materialization.InputHash != run.InputHash || materialization.FingerprintHash != run.FingerprintHash {
		return contract.VerificationSource{}, ErrSimulationRunInvalid
	}
	return contract.VerificationSource{RunID: run.ID, ProjectID: run.ProjectID, RevisionID: run.RevisionID, ScenarioDefinitionID: run.ScenarioDefinitionID, InputHash: run.InputHash, FingerprintHash: run.FingerprintHash}, nil
}

var _ contract.RevisionSource = (*Store)(nil)
var _ contract.ReleaseSource = (*Store)(nil)
var _ contract.ScenarioSource = (*Store)(nil)
var _ contract.VerificationSourceReader = (*Store)(nil)
