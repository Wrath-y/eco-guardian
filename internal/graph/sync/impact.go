package sync

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrImpactHandoffInvalid = errors.New("graph impact handoff is invalid")

// ImpactHandoff is the durable ready-to-impact boundary. The impact module is
// optional; a missing scheduler leaves the persisted handoff queued rather
// than changing Graph readiness or claiming downstream completion.
type ImpactHandoff struct {
	RevisionID domain.ID
	GraphHash  string
	Stage      string
}

func (h ImpactHandoff) Valid() bool {
	return h.RevisionID.Valid() && validHash(h.GraphHash) && h.Stage == "impact"
}

type ImpactScheduler interface {
	EnqueueImpact(context.Context, ImpactHandoff) error
}

type ImpactHandoffService struct{ Scheduler ImpactScheduler }

// Dispatch returns false without error when #9 is not installed. Persistence
// occurs in the Graph-ready transaction before this optional notification.
func (s ImpactHandoffService) Dispatch(ctx context.Context, handoff ImpactHandoff) (bool, error) {
	if !handoff.Valid() {
		return false, ErrImpactHandoffInvalid
	}
	if s.Scheduler == nil {
		return false, nil
	}
	if err := s.Scheduler.EnqueueImpact(ctx, handoff); err != nil {
		return false, err
	}
	return true, nil
}
