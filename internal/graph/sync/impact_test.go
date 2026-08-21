package sync

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type impactSchedulerFake struct{ handoffs []ImpactHandoff }

func (f *impactSchedulerFake) EnqueueImpact(_ context.Context, handoff ImpactHandoff) error {
	f.handoffs = append(f.handoffs, handoff)
	return nil
}

func TestImpactHandoffRemainsQueuedWhenImpactModuleIsUnavailable(t *testing.T) {
	revisionID, _ := domain.NewID()
	handoff := ImpactHandoff{RevisionID: revisionID, GraphHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Stage: "impact"}
	if dispatched, err := (ImpactHandoffService{}).Dispatch(context.Background(), handoff); err != nil || dispatched {
		t.Fatalf("dispatched=%v err=%v", dispatched, err)
	}
	scheduler := &impactSchedulerFake{}
	if dispatched, err := (ImpactHandoffService{Scheduler: scheduler}).Dispatch(context.Background(), handoff); err != nil || !dispatched || len(scheduler.handoffs) != 1 {
		t.Fatalf("dispatched=%v handoffs=%#v err=%v", dispatched, scheduler.handoffs, err)
	}
}
