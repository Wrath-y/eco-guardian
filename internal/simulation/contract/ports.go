// Package contract defines transport-neutral contracts shared by simulation
// orchestration and its adapters. It intentionally has no optional capability
// dependencies, so simulations remain usable without Graph or AI modules.
package contract

import (
	"context"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

type ID string

type Revision struct {
	ID           ID
	ProjectID    ID
	ConfigHash   string
	ManifestHash string
}

type ValidationGate interface {
	RequireFull(context.Context, ID) error
}

type RevisionSource interface {
	ResolveRevision(context.Context, ID) (Revision, error)
}

// ReleaseSource resolves an immutable release record to the revision it pinned
// when it was created. It must not read a mutable active-release pointer.
type ReleaseSource interface {
	ResolveReleaseRevision(context.Context, ID) (Revision, error)
}

// CapturedScenario ties a persisted immutable definition identity to the
// canonical template consumed by input normalization.
type CapturedScenario struct {
	DefinitionID domain.ID
	Template     scenario.Template
}

type ScenarioSource interface {
	ResolveSimulationScenario(context.Context, string, string) (CapturedScenario, error)
}

type ScenarioStore interface {
	GetScenario(context.Context, ID) ([]byte, error)
}

type RunStore interface {
	SaveRun(context.Context, ID, []byte) error
}

type CheckpointStore interface {
	LoadCheckpoint(context.Context, ID) ([]byte, error)
	SaveCheckpoint(context.Context, ID, []byte) error
}

type JobStore interface {
	CreateJob(context.Context, ID, string, string) (ID, error)
	RequestCancellation(context.Context, ID) (bool, error)
}

type EventStore interface {
	AppendEvent(context.Context, ID, []byte) error
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID() (ID, error)
}

type BoundedExecutor interface {
	Run(context.Context, int, func(context.Context, int) error) error
}
