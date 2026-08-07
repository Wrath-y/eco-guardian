package diff

import (
	"context"
	"encoding/json"

	"github.com/zouyi/eco-guardian/internal/domain"
)

// EntityBlob is the immutable entity materialization consumed by diff. It is
// intentionally independent of the physical blob and manifest tables.
type EntityBlob struct {
	EntityID domain.ID
	Kind     domain.EntityKind
	Status   domain.EntityStatus
	JSON     json.RawMessage
}

// ManifestReader streams a single revision's immutable entity materialization.
// Adapters must reject unreadable and cross-project revision identities.
type ManifestReader interface {
	Materialize(context.Context, domain.ID) ([]EntityBlob, error)
}

// ManifestSegmentReader reads a bounded range of one immutable manifest. The
// range starts strictly after afterEntityID and remains sorted by EntityID.
// It is intentionally read-only so diff work never needs the save write lane.
type ManifestSegmentReader interface {
	MaterializeSegment(ctx context.Context, revisionID, afterEntityID domain.ID, limit int) ([]EntityBlob, error)
}

// ActiveBaselineReader resolves only the revision referenced by the active
// release pointer. found=false means the project has no release baseline; an
// adapter must not substitute working state, history, or its latest revision.
type ActiveBaselineReader interface {
	ActiveBaseline(context.Context) (revisionID domain.ID, found bool, err error)
}
