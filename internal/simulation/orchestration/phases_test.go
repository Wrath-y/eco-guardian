package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type eventStoreFake struct{ event sharedjob.Event }

func (fake *eventStoreFake) Append(_ context.Context, event sharedjob.Event) (sharedjob.Event, bool, error) {
	fake.event = event
	return event, false, nil
}
func (fake *eventStoreFake) ListEvents(context.Context, domain.ID, int64) ([]sharedjob.Event, error) {
	return nil, nil
}

func TestPhasesAreMonotonicAndPersistedAsSharedJobEvents(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	store := &eventStoreFake{}
	previous := Phase("")
	for ordinal, next := range []Phase{PhaseQueued, PhaseMaterialized, PhaseSamplesRunning, PhaseAggregating, PhaseSealing, PhaseSucceeded} {
		event, _, appendErr := AppendPhase(context.Background(), store, id, int64(ordinal+1), previous, next, (ordinal+1)*10, "", "", time.Now().UTC())
		if appendErr != nil || event.Phase != string(next) || store.event.Ordinal != int64(ordinal+1) {
			t.Fatalf("event=%#v err=%v", event, appendErr)
		}
		previous = next
	}
	if _, _, err = AppendPhase(context.Background(), store, id, 7, PhaseQueued, PhaseSealing, 10, "", "", time.Now().UTC()); err != ErrSimulationPhase {
		t.Fatalf("invalid transition err=%v", err)
	}
}
