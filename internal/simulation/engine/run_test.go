package engine

import (
	"errors"
	"testing"
)

func testEvent(timeMS int64, source string, ordinal uint64) Event {
	return Event{TimeMS: timeMS, SourceID: source, InsertionOrdinal: ordinal, Kind: "damage", Version: "v1", EvaluatorID: "combat", TargetID: "target"}
}

func TestRunAdvancesByEventAndStopsAtSceneBoundary(t *testing.T) {
	queue := NewQueue()
	for _, event := range []Event{testEvent(0, "source", 0), testEvent(10, "source", 1), testEvent(11, "source", 2)} {
		if err := queue.Enqueue(event); err != nil {
			t.Fatal(err)
		}
	}
	order := []int64{}
	stats, err := Run(queue, Limits{DurationMS: 10, MaxEvents: 10, MaxSteps: 10}, func(event Event, _ *Emitter) error { order = append(order, event.TimeMS); return nil })
	if err != nil || stats.NowMS != 10 || stats.EventsExecuted != 2 || !stats.ReachedSceneEnd || len(order) != 2 {
		t.Fatalf("stats=%#v order=%v err=%v", stats, order, err)
	}
}

func TestRunRejectsPastEventsAndBudgets(t *testing.T) {
	queue := NewQueue()
	if err := queue.Enqueue(testEvent(2, "source", 0)); err != nil {
		t.Fatal(err)
	}
	_, err := Run(queue, Limits{DurationMS: 10, MaxEvents: 10, MaxSteps: 10}, func(_ Event, emitter *Emitter) error { return emitter.Schedule(testEvent(1, "derived", 0)) })
	if !errors.Is(err, ErrPastEvent) {
		t.Fatalf("past err=%v", err)
	}
	queue = NewQueue()
	if err = queue.Enqueue(testEvent(0, "source", 0)); err != nil {
		t.Fatal(err)
	}
	_, err = Run(queue, Limits{DurationMS: 10, MaxEvents: 1, MaxSteps: 10}, func(_ Event, emitter *Emitter) error { return emitter.Schedule(testEvent(0, "derived", 0)) })
	if !errors.Is(err, ErrEventBudget) {
		t.Fatalf("budget err=%v", err)
	}
}

func TestRunChecksAtEveryEventBoundary(t *testing.T) {
	queue := NewQueue()
	for ordinal := uint64(0); ordinal < 3; ordinal++ {
		if err := queue.Enqueue(testEvent(int64(ordinal), "source", ordinal)); err != nil {
			t.Fatal(err)
		}
	}
	stop := errors.New("canceled")
	checks := 0
	stats, err := RunWithCheckpoint(queue, Limits{DurationMS: 10, MaxEvents: 10, MaxSteps: 10}, func(Event, *Emitter) error { return nil }, func(Stats) error {
		checks++
		if checks == 3 {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) || stats.EventsExecuted != 2 || checks != 3 {
		t.Fatalf("stats=%#v checks=%d err=%v", stats, checks, err)
	}
}
