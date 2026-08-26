package graphprocess

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

func TestChildOutputIsWrappedRedactedAndTruncatedWithoutJSONTrust(t *testing.T) {
	events := make(chan ChildOutputEvent, 2)
	queue, err := NewChildOutputQueue(ChildOutputOptions{
		Capacity: 2, LineLimit: 80, Clock: func() time.Time { return time.Date(2026, 8, 25, 2, 0, 0, 0, time.UTC) },
		Sink: ChildEventSinkFunc(func(event ChildOutputEvent) { events <- event }),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close(t.Context())
	queue.PublishOutput(platformprocess.Output{
		Stream: platformprocess.StreamStderr, Generation: 4,
		Line: `{"event_name":"privileged","authorization":"Bearer fixture-secret","path":"C:\\Users\\alice\\project.db"}`,
	})
	select {
	case event := <-events:
		if event.EventName != "child_output" || event.Component != "local-rag" || event.Generation != 4 {
			t.Fatalf("event=%#v", event)
		}
		for _, forbidden := range []string{"fixture-secret", `C:\\Users`} {
			if strings.Contains(event.Line, forbidden) {
				t.Fatalf("unsafe event=%#v", event)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("event not published")
	}
	queue.PublishOutput(platformprocess.Output{Stream: platformprocess.StreamStdout, Generation: 4, Line: strings.Repeat("界", 100)})
	select {
	case event := <-events:
		if !event.Truncated || len(event.Line) > 80 || !utf8.ValidString(event.Line) {
			t.Fatalf("truncated event=%#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("truncated event not published")
	}
}

func TestChildOutputQueueDropsInsteadOfBlockingProducer(t *testing.T) {
	block := make(chan struct{})
	queue, err := NewChildOutputQueue(ChildOutputOptions{Capacity: 1, LineLimit: 128, Sink: ChildEventSinkFunc(func(ChildOutputEvent) { <-block })})
	if err != nil {
		t.Fatal(err)
	}
	queue.PublishOutput(platformprocess.Output{Stream: platformprocess.StreamStdout, Generation: 1, Line: "first"})
	time.Sleep(5 * time.Millisecond)
	for index := 0; index < 100; index++ {
		queue.PublishOutput(platformprocess.Output{Stream: platformprocess.StreamStdout, Generation: 1, Line: "flood"})
	}
	if queue.Dropped() == 0 {
		t.Fatal("flood did not report backpressure drops")
	}
	close(block)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err = queue.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSafeOutputSanitizerCoversCommonSecretFormsAndUserPaths(t *testing.T) {
	line := (SafeOutputSanitizer{}).SanitizeChildLine("token=abc password:xyz Authorization=Bearer.def /Users/alice/private C:\\Users\\alice\\private")
	for _, forbidden := range []string{"abc", "xyz", "Bearer.def", "alice", "private"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("sanitized line contains %q: %s", forbidden, line)
		}
	}
}
