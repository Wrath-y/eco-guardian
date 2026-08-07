package revision

import (
	"context"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

// Clock and IDGenerator make time and UUID allocation deterministic at the
// domain boundary. Concrete clocks and UUIDv7 generators live in adapters.
type Clock interface{ Now() time.Time }
type IDGenerator interface{ New() (domain.ID, error) }

// TransactionRunner lets revision operations atomically extend the existing
// config save transaction without importing a storage implementation.
type TransactionRunner interface {
	Within(context.Context, func(context.Context) error) error
}

// MetadataRepository exposes immutable, materialized revision metadata.
// Cursor ordering is fixed by display revision and immutable revision ID.
type MetadataRepository interface {
	GetRevisionRecord(context.Context, domain.ID) (Record, error)
	ListRevisionRecords(context.Context, string, int) (HistoryPage, error)
	GetRevisionDetail(context.Context, domain.ID) (Detail, error)
}

// RegistrationState records whether an implementation was available when a
// revision was written. Unregistered historical capabilities must not be
// silently replaced by a current implementation.
type RegistrationState string

const (
	Registered   RegistrationState = "registered"
	Unregistered RegistrationState = "unregistered"
)

// VersionContributor supplies one immutable interpretation identity. The
// registry and manifest materialization are deliberately domain concerns.
type VersionContributor interface {
	CapabilityID() string
	ContractVersion() string
	ImplementationVersion() string
	RegistrationState() RegistrationState
}

// VersionContributorSource is the composition-root boundary for registered
// contributors. It has no knowledge of SQLite, HTTP, or external services.
type VersionContributorSource interface {
	Contributors(context.Context) ([]VersionContributor, error)
}
