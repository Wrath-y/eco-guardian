package engine

import "errors"

var (
	ErrPastEvent      = errors.New("simulation event scheduled in the past")
	ErrEventBudget    = errors.New("simulation event budget exceeded")
	ErrStepBudget     = errors.New("simulation step budget exceeded")
	ErrExecutionLimit = errors.New("simulation execution limit is invalid")
)

type Limits struct {
	DurationMS          int64
	MaxEvents, MaxSteps int
}

func (limits Limits) Valid() bool {
	return limits.DurationMS > 0 && limits.MaxEvents > 0 && limits.MaxSteps > 0
}

type Stats struct {
	NowMS                 int64
	EventsExecuted, Steps int
	ReachedSceneEnd       bool
}

// Evaluator receives one event and may schedule finite derived events through
// the provided emitter. It never receives the queue or a clock.
type Evaluator func(Event, *Emitter) error

// Checkpoint runs at each event boundary. Orchestration uses it for bounded
// cancellation and deadline checks; it is deliberately outside domain state.
type Checkpoint func(Stats) error
type Emitter struct {
	queue     *Queue
	nowMS     int64
	allocator OrdinalAllocator
}

func (emitter *Emitter) Schedule(event Event) error {
	if event.TimeMS < emitter.nowMS {
		return ErrPastEvent
	}
	ordinal, err := emitter.allocator.Next()
	if err != nil {
		return err
	}
	event.InsertionOrdinal = ordinal
	return emitter.queue.Enqueue(event)
}

func Run(queue *Queue, limits Limits, evaluate Evaluator) (Stats, error) {
	return RunWithCheckpoint(queue, limits, evaluate, nil)
}

func RunWithCheckpoint(queue *Queue, limits Limits, evaluate Evaluator, checkpoint Checkpoint) (Stats, error) {
	if queue == nil || evaluate == nil || !limits.Valid() {
		return Stats{}, ErrExecutionLimit
	}
	emitter := &Emitter{queue: queue, allocator: OrdinalAllocator{next: uint64(queue.Len())}}
	stats := Stats{}
	for {
		if checkpoint != nil {
			if err := checkpoint(stats); err != nil {
				return stats, err
			}
		}
		event, found := queue.Pop()
		if !found {
			return stats, nil
		}
		if event.TimeMS > limits.DurationMS {
			stats.ReachedSceneEnd = true
			return stats, nil
		}
		if event.TimeMS < stats.NowMS {
			return stats, ErrPastEvent
		}
		if stats.EventsExecuted >= limits.MaxEvents {
			return stats, ErrEventBudget
		}
		if stats.Steps >= limits.MaxSteps {
			return stats, ErrStepBudget
		}
		stats.NowMS = event.TimeMS
		emitter.nowMS = stats.NowMS
		if err := evaluate(event, emitter); err != nil {
			return stats, normalizeEvaluatorError(err)
		}
		stats.EventsExecuted++
		stats.Steps++
	}
}
