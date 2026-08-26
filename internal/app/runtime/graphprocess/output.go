package graphprocess

import (
	"context"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

const defaultChildLineLimit = 8 * 1024

type ChildOutputEvent struct {
	EventName     string                           `json:"event_name"`
	Component     string                           `json:"component"`
	Stream        platformprocess.Stream           `json:"stream"`
	Generation    platformprocess.LaunchGeneration `json:"launch_generation"`
	Line          string                           `json:"line"`
	Truncated     bool                             `json:"truncated"`
	DroppedBefore uint64                           `json:"dropped_before,omitempty"`
	ObservedAt    time.Time                        `json:"observed_at"`
}

type ChildEventSink interface {
	PublishChildEvent(ChildOutputEvent)
}

type ChildEventSinkFunc func(ChildOutputEvent)

func (sink ChildEventSinkFunc) PublishChildEvent(value ChildOutputEvent) { sink(value) }

type OutputSanitizer interface {
	SanitizeChildLine(string) string
}

type ChildOutputOptions struct {
	Capacity  int
	LineLimit int
	Clock     func() time.Time
	Sanitizer OutputSanitizer
	Sink      ChildEventSink
}

type ChildOutputQueue struct {
	queue     chan ChildOutputEvent
	cancel    context.CancelFunc
	done      chan struct{}
	lineLimit int
	now       func() time.Time
	sanitizer OutputSanitizer
	sink      ChildEventSink
	dropped   atomic.Uint64
	closed    atomic.Bool
}

func NewChildOutputQueue(options ChildOutputOptions) (*ChildOutputQueue, error) {
	if options.Sink == nil || options.Capacity < 1 || options.Capacity > 4096 {
		return nil, ErrSupervisorInvalid
	}
	if options.LineLimit == 0 {
		options.LineLimit = defaultChildLineLimit
	}
	if options.LineLimit < 64 || options.LineLimit > 64*1024 {
		return nil, ErrSupervisorInvalid
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Sanitizer == nil {
		options.Sanitizer = SafeOutputSanitizer{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	queue := &ChildOutputQueue{
		queue: make(chan ChildOutputEvent, options.Capacity), cancel: cancel, done: make(chan struct{}),
		lineLimit: options.LineLimit, now: options.Clock, sanitizer: options.Sanitizer, sink: options.Sink,
	}
	go queue.run(ctx)
	return queue, nil
}

func (queue *ChildOutputQueue) PublishOutput(output platformprocess.Output) {
	if queue == nil || queue.closed.Load() || output.Generation == 0 || output.Stream != platformprocess.StreamStdout && output.Stream != platformprocess.StreamStderr {
		return
	}
	line := queue.sanitizer.SanitizeChildLine(output.Line)
	line, truncated := truncateUTF8(line, queue.lineLimit)
	event := ChildOutputEvent{
		EventName: "child_output", Component: "local-rag", Stream: output.Stream, Generation: output.Generation,
		Line: line, Truncated: output.Truncated || truncated, ObservedAt: queue.now().UTC(),
	}
	event.DroppedBefore = queue.dropped.Swap(0)
	select {
	case queue.queue <- event:
	default:
		queue.dropped.Add(event.DroppedBefore + 1)
	}
}

func (queue *ChildOutputQueue) Dropped() uint64 {
	if queue == nil {
		return 0
	}
	return queue.dropped.Load()
}

func (queue *ChildOutputQueue) Close(ctx context.Context) error {
	if queue == nil || !queue.closed.CompareAndSwap(false, true) {
		return nil
	}
	queue.cancel()
	select {
	case <-queue.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (queue *ChildOutputQueue) run(ctx context.Context) {
	defer close(queue.done)
	for {
		select {
		case event := <-queue.queue:
			queue.sink.PublishChildEvent(event)
		case <-ctx.Done():
			return
		}
	}
}

type SafeOutputSanitizer struct{}

var (
	secretAssignmentPattern = regexp.MustCompile(`(?i)(authorization|api[_-]?key|token|secret|password)\s*[:=]\s*([^\s,;]+)`)
	bearerPattern           = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]+`)
	windowsPathPattern      = regexp.MustCompile(`(?i)[a-z]:\\[^\r\n\t"']+`)
	userPathPattern         = regexp.MustCompile(`/(Users|home)/[^/\s]+(?:/[^\s"']*)?`)
)

func (SafeOutputSanitizer) SanitizeChildLine(value string) string {
	value = strings.ReplaceAll(value, "\x00", "")
	value = bearerPattern.ReplaceAllString(value, "Bearer <redacted>")
	value = secretAssignmentPattern.ReplaceAllString(value, "$1=<redacted>")
	value = windowsPathPattern.ReplaceAllString(value, "<path>")
	return userPathPattern.ReplaceAllString(value, "<path>")
}

func truncateUTF8(value string, maximum int) (string, bool) {
	if len(value) <= maximum {
		return value, false
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value, true
}

var _ platformprocess.OutputSink = (*ChildOutputQueue)(nil)
