package engine

import (
	"math"
	"testing"

	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

func TestQueueUsesCanonicalTotalOrderIndependentOfInsertionOrder(t *testing.T) {
	events := []Event{
		{TimeMS: 10, RulePriority: 2, SourceID: "z", InsertionOrdinal: 1, Kind: "damage", Version: "v1", EvaluatorID: "combat", TargetID: "target"},
		{TimeMS: 10, RulePriority: 1, SourceID: "z", InsertionOrdinal: 1, Kind: "damage", Version: "v1", EvaluatorID: "combat", TargetID: "target"},
		{TimeMS: 10, RulePriority: 2, SourceID: "a", InsertionOrdinal: 2, Kind: "damage", Version: "v1", EvaluatorID: "combat", TargetID: "target"},
		{TimeMS: 5, RulePriority: 9, SourceID: "z", InsertionOrdinal: 9, Kind: "damage", Version: "v1", EvaluatorID: "combat", TargetID: "target"},
		{TimeMS: 10, RulePriority: 2, SourceID: "a", InsertionOrdinal: 1, Kind: "damage", Version: "v1", EvaluatorID: "combat", TargetID: "target"},
	}
	queue := NewQueue()
	for index := len(events) - 1; index >= 0; index-- {
		if err := queue.Enqueue(events[index]); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"5/9/z/9", "10/1/z/1", "10/2/a/1", "10/2/a/2", "10/2/z/1"}
	for index, expected := range want {
		event, found := queue.Pop()
		if !found || event.String() != expected {
			t.Fatalf("index=%d event=%s found=%v want=%s", index, event.String(), found, expected)
		}
	}
}

func TestInitialEventsKeepDeclaredActionOrderAndOrdinalSequence(t *testing.T) {
	definition := scenario.BuiltinTemplates()[2].Definition
	events, err := InitialEvents(definition)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != len(definition.Actions) {
		t.Fatalf("events=%d", len(events))
	}
	for index, event := range events {
		if event.InsertionOrdinal != uint64(index) || event.Kind != definition.Actions[index].EventID || event.SourceID != definition.Actions[index].SourceID {
			t.Fatalf("index=%d event=%#v", index, event)
		}
	}
	allocator := OrdinalAllocator{next: math.MaxUint64}
	if _, err = allocator.Next(); err != ErrOrdinalOverflow {
		t.Fatalf("overflow err=%v", err)
	}
}

func TestQueueRejectsInvalidAndDuplicateCompleteKeys(t *testing.T) {
	queue := NewQueue()
	event := Event{TimeMS: 0, SourceID: "source", InsertionOrdinal: 1, Kind: "damage", Version: "v1", EvaluatorID: "combat", TargetID: "target"}
	if err := queue.Enqueue(event); err != nil {
		t.Fatal(err)
	}
	if err := queue.Enqueue(event); err != ErrDuplicateEvent {
		t.Fatalf("duplicate err=%v", err)
	}
	event.TimeMS = -1
	if err := queue.Enqueue(event); err != ErrEventInvalid {
		t.Fatalf("invalid err=%v", err)
	}
}

func FuzzQueueAndRunRemainBounded(f *testing.F) {
	f.Add(int64(0), "source", "damage")
	f.Add(int64(-1), "\x00", "")
	f.Fuzz(func(t *testing.T, at int64, source, kind string) {
		queue := NewQueue()
		event := Event{TimeMS: at, SourceID: source, InsertionOrdinal: 0, Kind: kind, Version: "v1", EvaluatorID: "combat", TargetID: "target"}
		if err := queue.Enqueue(event); err != nil {
			return
		}
		stats, err := Run(queue, Limits{DurationMS: 10, MaxEvents: 1, MaxSteps: 1}, func(_ Event, emitter *Emitter) error {
			return emitter.Schedule(testEvent(0, "derived", 0))
		})
		if stats.NowMS < 0 || stats.EventsExecuted > 1 || stats.Steps > 1 || (err == nil && queue.Len() > 0 && !stats.ReachedSceneEnd) {
			t.Fatalf("stats=%#v err=%v", stats, err)
		}
	})
}
