package validation

import (
	"context"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type MaterializedSource struct {
	Source   Source
	Entities []domain.Entity
}
type SourceReader interface {
	Materialize(context.Context, Source) (MaterializedSource, error)
}
type RunRepository interface {
	InsertCompleted(context.Context, ValidationRun, []Issue) error
	FindMatchingFull(context.Context, domain.ID, string, VersionManifest) (ValidationRun, bool, error)
}
type Clock interface{ Now() time.Time }
type Registry interface{ Manifest() VersionManifest }

// Gate has no override or free-text argument by design.
type Gate interface {
	Check(context.Context, domain.ID, string, VersionManifest) (GateResult, error)
}
