package engine

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

var (
	ErrEventInvalid    = errors.New("simulation event is invalid")
	ErrDuplicateEvent  = errors.New("simulation event has duplicate ordering key")
	ErrOrdinalOverflow = errors.New("simulation event insertion ordinal overflow")
)

// Event is a canonical discrete-event DTO. Payload is an immutable canonical
// byte sequence; callers must not attach callbacks or executable objects.
type Event struct {
	TimeMS           int64
	RulePriority     int
	SourceID         string
	InsertionOrdinal uint64
	Kind             string
	Version          string
	EvaluatorID      string
	TargetID         string
	Payload          []byte
}

func (event Event) Valid() bool {
	return event.TimeMS >= 0 && stableID(event.SourceID) && stableID(event.Kind) && stableID(event.Version) && stableID(event.EvaluatorID) && stableID(event.TargetID)
}

type Queue struct {
	events eventHeap
	keys   map[eventKey]struct{}
}

func NewQueue() *Queue {
	queue := &Queue{keys: map[eventKey]struct{}{}}
	heap.Init(&queue.events)
	return queue
}
func (q *Queue) Len() int { return q.events.Len() }

func (q *Queue) Enqueue(event Event) error {
	if !event.Valid() {
		return ErrEventInvalid
	}
	key := eventKey{event.TimeMS, event.RulePriority, event.SourceID, event.InsertionOrdinal}
	if _, duplicate := q.keys[key]; duplicate {
		return ErrDuplicateEvent
	}
	event.Payload = append([]byte(nil), event.Payload...)
	heap.Push(&q.events, event)
	q.keys[key] = struct{}{}
	return nil
}

func (q *Queue) Pop() (Event, bool) {
	if q.events.Len() == 0 {
		return Event{}, false
	}
	event := heap.Pop(&q.events).(Event)
	delete(q.keys, eventKey{event.TimeMS, event.RulePriority, event.SourceID, event.InsertionOrdinal})
	event.Payload = append([]byte(nil), event.Payload...)
	return event, true
}

type eventKey struct {
	timeMS   int64
	priority int
	sourceID string
	ordinal  uint64
}
type eventHeap []Event

func (events eventHeap) Len() int                  { return len(events) }
func (events eventHeap) Less(left, right int) bool { return compare(events[left], events[right]) < 0 }
func (events eventHeap) Swap(left, right int) {
	events[left], events[right] = events[right], events[left]
}
func (events *eventHeap) Push(value any) { *events = append(*events, value.(Event)) }
func (events *eventHeap) Pop() any {
	old := *events
	last := len(old) - 1
	value := old[last]
	*events = old[:last]
	return value
}

func compare(left, right Event) int {
	if left.TimeMS != right.TimeMS {
		if left.TimeMS < right.TimeMS {
			return -1
		}
		return 1
	}
	if left.RulePriority != right.RulePriority {
		if left.RulePriority < right.RulePriority {
			return -1
		}
		return 1
	}
	if left.SourceID != right.SourceID {
		if left.SourceID < right.SourceID {
			return -1
		}
		return 1
	}
	if left.InsertionOrdinal < right.InsertionOrdinal {
		return -1
	}
	if left.InsertionOrdinal > right.InsertionOrdinal {
		return 1
	}
	return 0
}

func stableID(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}

func (event Event) String() string {
	return fmt.Sprintf("%d/%d/%s/%d", event.TimeMS, event.RulePriority, event.SourceID, event.InsertionOrdinal)
}

// OrdinalAllocator is sample-local. Initial events follow the declared action
// sequence; evaluator-derived events must request ordinals in their declared
// derivation sequence before they are enqueued.
type OrdinalAllocator struct{ next uint64 }

func (a *OrdinalAllocator) Next() (uint64, error) {
	if a.next == math.MaxUint64 {
		return 0, ErrOrdinalOverflow
	}
	value := a.next
	a.next++
	return value, nil
}

func InitialEvents(definition scenario.Definition) ([]Event, error) {
	if err := definition.Validate(); err != nil {
		return nil, err
	}
	allocator := &OrdinalAllocator{}
	events := make([]Event, 0, len(definition.Actions))
	for _, action := range definition.Actions {
		ordinal, err := allocator.Next()
		if err != nil {
			return nil, err
		}
		events = append(events, Event{TimeMS: action.AtMS, RulePriority: 0, SourceID: action.SourceID, InsertionOrdinal: ordinal, Kind: action.EventID, Version: "v1", EvaluatorID: action.EvaluatorID, TargetID: action.TargetID})
	}
	return events, nil
}
