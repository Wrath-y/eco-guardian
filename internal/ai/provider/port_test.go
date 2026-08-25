package provider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

func providerIdentity(id string) aicontract.VersionIdentity {
	return aicontract.VersionIdentity{ID: id, Version: "v1", Hash: aicontract.Hash("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}
}

func validAttemptRequest(t *testing.T) AttemptRequest {
	t.Helper()
	tool := ToolDefinition{Identity: providerIdentity("read_revision_context"), Description: "Read frozen revision context", InputSchema: json.RawMessage(`{"type":"object"}`)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return AttemptRequest{
		Manifest: AttemptManifest{
			AttemptID: "attempt-1", Provider: providerIdentity("openai-compatible"), Model: providerIdentity("fixture-model"),
			EndpointClassification: EndpointLoopback, Prompt: providerIdentity("prompt"), StructuredResponseSchema: providerIdentity("draft-patch"),
			Tools: []aicontract.VersionIdentity{tool.Identity}, Orchestrator: providerIdentity("orchestrator"), Budget: providerIdentity("budget"),
			InputHash:            aicontract.Hash("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
			EvidenceManifestHash: aicontract.Hash("cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"), CancelGeneration: 3,
		},
		Prompt: PromptMessages{System: "system", Developer: "developer"}, UserInput: json.RawMessage(`{"goal":"safe"}`),
		EvidenceSummary: json.RawMessage(`{"evidence_ids":["e-1"]}`), ResponseSchema: json.RawMessage(`{"type":"object"}`),
		Tools: []ToolDefinition{tool}, Parameters: ModelParameters{Temperature: "0", TopP: "1"}, Timeout: time.Second,
		Limits:       aicontract.V1Fixture().Budget.Limits,
		Cancellation: ContextCancellation{Context: ctx, CancelGeneration: 3},
	}
}

func TestAttemptRequestContainsOnlyTransportNeutralFixedContracts(t *testing.T) {
	request := validAttemptRequest(t)
	if !request.Valid() {
		t.Fatalf("valid request rejected: %#v", request)
	}
	request.Manifest.CancelGeneration++
	if request.Valid() {
		t.Fatal("mismatched cancellation generation accepted")
	}
	original := validAttemptRequest(t)
	clone := original.Clone()
	clone.Tools[0].InputSchema[0] = '['
	if string(original.Tools[0].InputSchema) != `{"type":"object"}` {
		t.Fatal("request clone retained mutable JSON aliases")
	}
}

func TestAttemptOrderedSinkRejectsUnregisteredToolsAndSchemaDrift(t *testing.T) {
	request := validAttemptRequest(t)
	sink, err := NewAttemptOrderedSink(request.Manifest, EventSinkFunc(func(Event) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	unregistered := Event{Sequence: 1, Type: EventToolCall, ToolCall: &ToolCall{CallID: "call-1", Tool: providerIdentity("write_revision"), Arguments: json.RawMessage(`{}`)}}
	if err := sink.Emit(unregistered); !errors.Is(err, ErrEventInvalid) {
		t.Fatalf("unregistered tool error=%v", err)
	}
	drifted := Event{Sequence: 1, Type: EventStructuredResponse, StructuredResponse: &StructuredResponse{Schema: providerIdentity("other-schema"), Body: json.RawMessage(`{}`), BodyHash: aicontract.Hash("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")}}
	if err := sink.Emit(drifted); !errors.Is(err, ErrEventInvalid) {
		t.Fatalf("schema drift error=%v", err)
	}
}

func TestOrderedSinkAcceptsUsageToolAndOneTerminalStructuredResponse(t *testing.T) {
	var events []Event
	sink := NewOrderedSink(EventSinkFunc(func(event Event) error {
		events = append(events, event)
		return nil
	}))
	for _, event := range []Event{
		{Sequence: 1, Type: EventUsage, Usage: &Usage{InputTokens: 10, OutputTokens: 0, TotalTokens: 10}},
		{Sequence: 2, Type: EventToolCall, ToolCall: &ToolCall{CallID: "call-1", Tool: providerIdentity("read_revision_context"), Arguments: json.RawMessage(`{}`)}},
		{Sequence: 3, Type: EventWarning, Warning: &Warning{Code: "PROVIDER_DEGRADED", Message: "Streaming usage was delayed"}},
		{Sequence: 4, Type: EventStructuredResponse, StructuredResponse: &StructuredResponse{Schema: providerIdentity("draft-patch"), Body: json.RawMessage(`{"targets":[]}`), BodyHash: aicontract.Hash("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd")}},
	} {
		if err := sink.Emit(event); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 4 {
		t.Fatalf("events=%#v", events)
	}
	if err := sink.Emit(Event{Sequence: 5, Type: EventWarning, Warning: &Warning{Code: "LATE", Message: "late"}}); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("post-terminal event error=%v", err)
	}
}

func TestOrderedSinkRejectsGapsInvalidPayloadsAndConflictingTerminals(t *testing.T) {
	sink := NewOrderedSink(EventSinkFunc(func(Event) error { return nil }))
	if err := sink.Emit(Event{Sequence: 2, Type: EventWarning, Warning: &Warning{Code: "GAP", Message: "gap"}}); !errors.Is(err, ErrEventOrder) {
		t.Fatalf("gap error=%v", err)
	}
	if err := sink.Emit(Event{Sequence: 1, Type: EventUsage, Warning: &Warning{Code: "WRONG", Message: "wrong payload"}}); !errors.Is(err, ErrEventInvalid) {
		t.Fatalf("invalid payload error=%v", err)
	}
	terminal := Event{Sequence: 1, Type: EventError, Error: &TerminalError{Code: "AI_PROVIDER_TIMEOUT", Class: ErrorTimeout, Retryable: true, Message: "Provider timed out"}}
	if err := sink.Emit(terminal); err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(terminal); !errors.Is(err, ErrTerminalConflict) {
		t.Fatalf("duplicate terminal error=%v", err)
	}
}
