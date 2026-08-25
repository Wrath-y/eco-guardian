package orchestration

import (
	"context"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/risk/contract"
)

type Clock interface{ Now() time.Time }
type IDGenerator interface{ New() (domain.ID, error) }
type BoundedExecutor interface {
	Run(context.Context, int, func(context.Context, int) error) error
}
type JobStore = sharedjob.Store
type EventStore = sharedjob.EventStore

type SimulationRunReader interface {
	RiskSimulationRun(context.Context, domain.ID) (contract.RunEvidence, error)
}

type ImpactEvidenceReader interface {
	Evidence(context.Context, domain.ID, domain.ID) ([]contract.ImpactEvidenceRef, error)
}
