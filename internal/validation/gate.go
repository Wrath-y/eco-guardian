package validation

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type FullRunFinder interface {
	FindMatchingFullRun(context.Context, domain.ID, string, VersionManifest) (ValidationRun, bool, error)
}
type ValidationGate struct{ runs FullRunFinder }

func NewValidationGate(runs FullRunFinder) *ValidationGate { return &ValidationGate{runs: runs} }
func (g *ValidationGate) Check(ctx context.Context, revisionID domain.ID, configHash string, versions VersionManifest) (GateResult, error) {
	run, found, err := g.runs.FindMatchingFullRun(ctx, revisionID, configHash, versions)
	if err != nil {
		return GateRequiresValidation, err
	}
	if !found {
		return GateRequiresValidation, nil
	}
	if run.Scope != ScopeFull || run.Source.Kind != SourceRevision || run.Source.RevisionID != revisionID || run.Source.InputHash != configHash || run.Versions != versions {
		return GateRequiresValidation, nil
	}
	if run.Summary.Error > 0 || run.Summary.Block > 0 {
		return GateBlocked, nil
	}
	return GatePass, nil
}
