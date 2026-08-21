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
