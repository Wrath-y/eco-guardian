package provider

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

func TestOnlyOneMatchingTerminalStructuredResponseCanAdvanceAttempt(t *testing.T) {
	request := validAttemptRequest(t)
	sealed := 0
	sink, err := NewAttemptOrderedSink(request.Manifest, EventSinkFunc(func(event Event) error {
		if event.Type == EventStructuredResponse {
			sealed++
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, partial := range []Event{
		{Sequence: 1, Type: EventUsage, Usage: &Usage{InputTokens: 1, TotalTokens: 1}},
		{Sequence: 2, Type: EventWarning, Warning: &Warning{Code: "PARTIAL_IGNORED", Message: `partial model text {"targets":`}},
		{Sequence: 3, Type: EventToolCall, ToolCall: &ToolCall{CallID: "call-1", Tool: request.Manifest.Tools[0], Arguments: json.RawMessage(`{}`)}},
	} {
		if err := sink.Emit(partial); err != nil {
			t.Fatal(err)
		}
	}
	if sealed != 0 {
		t.Fatal("non-terminal Provider data advanced the attempt")
	}
	mismatch := Event{Sequence: 4, Type: EventStructuredResponse, StructuredResponse: &StructuredResponse{
		Schema: providerIdentity("other-schema"), Body: json.RawMessage(`{"targets":[]}`), BodyHash: aicontract.Hash("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"),
	}}
	if err := sink.Emit(mismatch); !errors.Is(err, ErrEventInvalid) || sealed != 0 {
		t.Fatalf("schema mismatch error=%v sealed=%d", err, sealed)
	}
	matching := mismatch
	matching.StructuredResponse = &StructuredResponse{
		Schema: request.Manifest.StructuredResponseSchema, Body: json.RawMessage(`{"targets":[]}`), BodyHash: aicontract.Hash("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"),
	}
	if err := sink.Emit(matching); err != nil || sealed != 1 {
		t.Fatalf("matching terminal error=%v sealed=%d", err, sealed)
	}
	if err := sink.Emit(Event{Sequence: 5, Type: EventStructuredResponse, StructuredResponse: matching.StructuredResponse}); !errors.Is(err, ErrTerminalConflict) || sealed != 1 {
		t.Fatalf("duplicate terminal error=%v sealed=%d", err, sealed)
	}
}

func TestPartialOrDisconnectedStreamNeverEmitsStructuredResponse(t *testing.T) {
	raw, err := os.ReadFile("openai/testdata/stream-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name           string   `json:"name"`
		ExpectedEvents []string `json:"expected_events"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"disconnect_without_done": false, "malformed_json": false, "late_terminal_after_cancel": false}
	for _, fixture := range fixtures {
		if _, found := want[fixture.Name]; !found {
			continue
		}
		want[fixture.Name] = true
		if strings.Join(fixture.ExpectedEvents, ",") != "error" {
			t.Errorf("fixture %s exposed partial/structured output: %v", fixture.Name, fixture.ExpectedEvents)
		}
	}
	for fixture, found := range want {
		if !found {
			t.Errorf("missing pinned partial-stream fixture %s", fixture)
		}
	}
	for _, eventType := range []EventType{EventUsage, EventToolCall, EventWarning, EventError, EventStructuredResponse} {
		if eventType == "partial" {
			t.Fatal("transport-neutral Provider contract exposed partial output")
		}
	}
}
