package sqlite

import (
	"context"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/validation"
)

// RunValidation materializes a source before analysis and keeps SQLite write
// time to the final immutable report insertion.
func (s *Store) RunValidation(ctx context.Context, kind validation.SourceKind, revisionID domain.ID, scope validation.Scope) (ValidationReport, error) {
	snapshot, err := s.MaterializeValidationSource(ctx, kind, revisionID)
	if err != nil {
		return ValidationReport{}, err
	}
	registry, err := formula.V1Registry()
	if err != nil {
		return ValidationReport{}, err
	}
	versions, err := validation.V1VersionManifest(registry)
	if err != nil {
		return ValidationReport{}, err
	}
	issues, err := validation.AnalyzeV1(ctx, s.registry, registry, versions, snapshot.Entities, scope)
	if err != nil {
		return ValidationReport{}, err
	}
	id, err := domain.NewID()
	if err != nil {
		return ValidationReport{}, err
	}
	run, err := validation.NewCompletedRun(id, snapshot.Source, scope, versions, issues, time.Now())
	if err != nil {
		return ValidationReport{}, err
	}
	if err = s.InsertCompletedValidationRun(ctx, run, issues); err != nil {
		return ValidationReport{}, err
	}
	return ValidationReport{Run: run, Issues: validation.CloneIssues(issues)}, nil
}

// RunFullValidation is the narrow Graph orchestration adapter over the
// existing revision-scoped #6 validation runner.
func (s *Store) RunFullValidation(ctx context.Context, revisionID domain.ID) error {
	_, err := s.RunValidation(ctx, validation.SourceRevision, revisionID, validation.ScopeFull)
	return err
}
