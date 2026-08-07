package policy

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/domain"
)

// ContractCatalog is the composition-boundary view of Gate contracts that a
// policy may require. Persistence never guesses unknown contracts.
type ContractCatalog interface {
	SupportsCapabilityContract(CapabilityRequirement) bool
}

type Page struct {
	Items      []ReleasePolicy
	NextCursor string
}

// Store is the immutable policy persistence boundary. It deliberately has no
// update or delete operation.
type Store interface {
	Insert(context.Context, domain.ID, []byte, string) error
	Get(context.Context, domain.ID) ([]byte, string, error)
}

type Repository interface {
	CreatePolicy(context.Context, Definition, ContractCatalog) (ReleasePolicy, error)
	GetPolicy(context.Context, domain.ID) (ReleasePolicy, error)
	ListPolicies(context.Context, string, int) (Page, error)
}
