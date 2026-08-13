package projector

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/domain"
)

// RevisionReader materializes exactly one immutable configuration revision for
// projection. Implementations must not substitute working state, another
// revision, or current projector defaults when the requested revision is
// unavailable.
type RevisionReader interface {
	ReadProjectionRevision(context.Context, domain.ID) (Revision, error)
}
