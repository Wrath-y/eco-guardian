package sync

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type impactSchedulerFake struct{ handoffs []ImpactHandoff }

type impactRecoveryStoreFake struct{ handoffs []ImpactHandoff }

func (fake impactRecoveryStoreFake) ListQueuedImpactHandoffs(context.Context, int) ([]ImpactHandoff, error) {
	return append([]ImpactHandoff(nil), fake.handoffs...), nil
}

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

func TestImpactRecoveryReplaysOnlyDurableExactHandoffs(t *testing.T) {
	revisionID, _ := domain.NewID()
	handoff := ImpactHandoff{RevisionID: revisionID, GraphHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Stage: "impact"}
	scheduler := &impactSchedulerFake{}
	results, err := (ImpactRecoveryService{Store: impactRecoveryStoreFake{handoffs: []ImpactHandoff{handoff}}, Scheduler: scheduler}).Recover(context.Background())
	if err != nil || len(results) != 1 || !results[0].Dispatched || len(scheduler.handoffs) != 1 || scheduler.handoffs[0] != handoff {
		t.Fatalf("results=%#v handoffs=%#v err=%v", results, scheduler.handoffs, err)
	}
	results, err = (ImpactRecoveryService{Store: impactRecoveryStoreFake{handoffs: []ImpactHandoff{handoff}}}).Recover(context.Background())
	if err != nil || results[0].Dispatched {
		t.Fatalf("missing module guessed recovery: %#v err=%v", results, err)
	}
}
