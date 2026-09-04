package sqlite

import (
	"context"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/validation"
)

type RebuildResult struct {
	RevisionID     domain.ID
	FormulaCount   int
	ReferenceCount int
}

// RebuildRevisionDerived rebuilds only disposable revision-scoped indexes from
// the immutable manifest. It never reads working rows and never changes blobs,
// revisions, or a validation run.
func (s *Store) RebuildRevisionDerived(ctx context.Context, revisionID domain.ID, registry *formula.Registry, symbols formula.SymbolResolver) (RebuildResult, error) {
	if registry == nil {
		return RebuildResult{}, fmt.Errorf("formula registry is required")
	}
	snapshot, err := s.MaterializeValidationSource(ctx, validation.SourceRevision, revisionID)
	if err != nil {
		return RebuildResult{}, err
	}
	references, bindings := validation.WalkKnownSchema(snapshot.Entities)
	formulas, diagnostics := validation.CompileFormulaBindings(bindings, registry, validation.AttributeSymbols(snapshot.Entities, registry, symbols))
	if len(diagnostics) > 0 {
		return RebuildResult{}, fmt.Errorf("cannot rebuild invalid formula indexes: %s", diagnostics[0].Code)
	}
	if err = s.ReplaceRevisionDerived(ctx, revisionID, formulas, references); err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{revisionID, len(formulas), len(references)}, nil
}
