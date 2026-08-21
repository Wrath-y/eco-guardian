package contract

import (
	"context"
	"testing"
	"time"
)

type fakeValidationGate struct{ err error }

func (f fakeValidationGate) RequireFull(context.Context, ID) error { return f.err }

type fakeRevisionSource struct {
	revision Revision
	err      error
}

func (f fakeRevisionSource) ResolveRevision(context.Context, ID) (Revision, error) {
	return f.revision, f.err
}

type fakeScenarioStore struct {
	body []byte
	err  error
}

func (f fakeScenarioStore) GetScenario(context.Context, ID) ([]byte, error) {
	return append([]byte(nil), f.body...), f.err
}

type fakeRunStore struct {
	saved []byte
	err   error
}

func (f *fakeRunStore) SaveRun(_ context.Context, _ ID, body []byte) error {
	f.saved = append([]byte(nil), body...)
	return f.err
}

type fakeCheckpointStore struct {
	body []byte
	err  error
}

func (f *fakeCheckpointStore) LoadCheckpoint(context.Context, ID) ([]byte, error) {
	return append([]byte(nil), f.body...), f.err
}
func (f *fakeCheckpointStore) SaveCheckpoint(_ context.Context, _ ID, body []byte) error {
	f.body = append([]byte(nil), body...)
	return f.err
}

type fakeJobStore struct {
	id      ID
	changed bool
	err     error
}

func (f fakeJobStore) CreateJob(context.Context, ID, string, string) (ID, error) { return f.id, f.err }
func (f fakeJobStore) RequestCancellation(context.Context, ID) (bool, error)     { return f.changed, f.err }

type fakeEventStore struct {
	event []byte
	err   error
}

func (f *fakeEventStore) AppendEvent(_ context.Context, _ ID, event []byte) error {
	f.event = append([]byte(nil), event...)
	return f.err
}

type fakeClock struct{ now time.Time }

func (f fakeClock) Now() time.Time { return f.now }

type fakeIDGenerator struct {
	id  ID
	err error
}

func (f fakeIDGenerator) NewID() (ID, error) { return f.id, f.err }

type fakeExecutor struct{ err error }

func (f fakeExecutor) Run(ctx context.Context, workers int, run func(context.Context, int) error) error {
	if f.err != nil {
		return f.err
	}
	for worker := 0; worker < workers; worker++ {
		if err := run(ctx, worker); err != nil {
			return err
		}
	}
	return nil
}

var (
	_ ValidationGate  = fakeValidationGate{}
	_ RevisionSource  = fakeRevisionSource{}
	_ ScenarioStore   = fakeScenarioStore{}
	_ RunStore        = (*fakeRunStore)(nil)
	_ CheckpointStore = (*fakeCheckpointStore)(nil)
	_ JobStore        = fakeJobStore{}
	_ EventStore      = (*fakeEventStore)(nil)
	_ Clock           = fakeClock{}
	_ IDGenerator     = fakeIDGenerator{}
	_ BoundedExecutor = fakeExecutor{}
)

func TestPortFakesRemainTransportNeutral(t *testing.T) {
	ctx := context.Background()
	runs := &fakeRunStore{}
	checkpoints := &fakeCheckpointStore{}
	events := &fakeEventStore{}
	if err := runs.SaveRun(ctx, "run", []byte("result")); err != nil {
		t.Fatal(err)
	}
	if err := checkpoints.SaveCheckpoint(ctx, "job", []byte("checkpoint")); err != nil {
		t.Fatal(err)
	}
	if err := events.AppendEvent(ctx, "job", []byte("event")); err != nil {
		t.Fatal(err)
	}
	if string(runs.saved) != "result" || string(checkpoints.body) != "checkpoint" || string(events.event) != "event" {
		t.Fatal("fake port state was not isolated")
	}
	called := 0
	if err := (fakeExecutor{}).Run(ctx, 2, func(context.Context, int) error { called++; return nil }); err != nil || called != 2 {
		t.Fatalf("called=%d err=%v", called, err)
	}
}
