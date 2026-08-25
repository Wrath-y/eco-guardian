package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

func adapterIdentity(id string) aicontract.VersionIdentity {
	return aicontract.VersionIdentity{ID: id, Version: "v1", Hash: aicontract.Hash(strings.Repeat("a", 64))}
}

func adapterRequest(t *testing.T) aiprovider.AttemptRequest {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	tool := aiprovider.ToolDefinition{Identity: adapterIdentity("read_revision_context"), Description: "Read context", InputSchema: json.RawMessage(`{"type":"object"}`)}
	return aiprovider.AttemptRequest{
		Manifest: aiprovider.AttemptManifest{
			AttemptID: "attempt-1", Provider: adapterIdentity("openai-compatible"), Model: adapterIdentity("fixture-model"),
			EndpointClassification: aiprovider.EndpointLoopback, Prompt: adapterIdentity("prompt"), StructuredResponseSchema: adapterIdentity("draft-patch"),
			Tools: []aicontract.VersionIdentity{tool.Identity}, Orchestrator: adapterIdentity("orchestrator"), Budget: adapterIdentity("budget"),
			InputHash: aicontract.Hash(strings.Repeat("b", 64)), EvidenceManifestHash: aicontract.Hash(strings.Repeat("c", 64)), CancelGeneration: 2,
		},
		Prompt:    aiprovider.PromptMessages{System: "system", Developer: "developer"},
		UserInput: json.RawMessage(`{"goal":"safe"}`), EvidenceSummary: json.RawMessage(`{"evidence_ids":["e-1"]}`),
		ResponseSchema: json.RawMessage(`{"type":"object"}`), Tools: []aiprovider.ToolDefinition{tool},
		Parameters: aiprovider.ModelParameters{Temperature: "0", TopP: "1"}, Timeout: time.Second,
		Limits:       aicontract.V1Fixture().Budget.Limits,
		Cancellation: aiprovider.ContextCancellation{Context: ctx, CancelGeneration: 2},
	}
}

func adapterForServer(t *testing.T, server *httptest.Server) *Adapter {
	t.Helper()
	resolver := aiprovider.CredentialResolver{Environment: testEnvironment{aiprovider.OpenAIAPIKeyEnvironment: "adapter-canary"}}
	secret, err := resolver.Resolve(context.Background(), aiprovider.OpenAICompatibleProvider)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewAdapter(AdapterConfig{
		Endpoint: server.URL + "/v1", Classification: aiprovider.EndpointLoopback, Model: "fixture-model", Credential: secret,
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func captureAttemptEvents(t *testing.T, request aiprovider.AttemptRequest) (*aiprovider.OrderedSink, *[]aiprovider.Event) {
	t.Helper()
	events := []aiprovider.Event{}
	sink, err := aiprovider.NewAttemptOrderedSink(request.Manifest, aiprovider.EventSinkFunc(func(event aiprovider.Event) error {
		events = append(events, event)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return sink, &events
}

func TestAdapterStreamsUsageAndOnlyTerminalStructuredResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer adapter-canary" {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body["stream"] != true || body["model"] != "fixture-model" {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = response.Write([]byte("data: {\"id\":\"one\",\"object\":\"chat.completion.chunk\",\"model\":\"fixture-model\",\"choices\":[{\"delta\":{\"content\":\"{\\\"targets\\\":\"},\"finish_reason\":null}]}\n\n"))
		_, _ = response.Write([]byte("data: {\"model\":\"fixture-model\",\"choices\":[{\"delta\":{\"content\":\"[]}\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = response.Write([]byte("data: {\"model\":\"fixture-model\",\"choices\":[],\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"total_tokens\":12}}\n\n"))
		_, _ = response.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	request := adapterRequest(t)
	sink, events := captureAttemptEvents(t, request)
	if err := adapterForServer(t, server).Invoke(request, sink); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 2 || (*events)[0].Type != aiprovider.EventUsage || (*events)[1].Type != aiprovider.EventStructuredResponse || string((*events)[1].StructuredResponse.Body) != `{"targets":[]}` {
		t.Fatalf("events=%#v", *events)
	}
}

func TestAdapterAssemblesRegisteredToolCallWithoutPartialEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = response.Write([]byte("data: {\"model\":\"fixture-model\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"function\":{\"name\":\"read_revision_context\",\"arguments\":\"{\\\"base_identity_hash\\\":\\\"x\\\",\"}}]},\"finish_reason\":null}]}\n\n"))
		_, _ = response.Write([]byte("data: {\"model\":\"fixture-model\",\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"payload\\\":{}}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		_, _ = response.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	request := adapterRequest(t)
	sink, events := captureAttemptEvents(t, request)
	if err := adapterForServer(t, server).Invoke(request, sink); err != nil {
		t.Fatal(err)
	}
	if len(*events) != 1 || (*events)[0].Type != aiprovider.EventToolCall || (*events)[0].ToolCall.Tool.ID != "read_revision_context" || string((*events)[0].ToolCall.Arguments) != `{"base_identity_hash":"x","payload":{}}` {
		t.Fatalf("events=%#v", *events)
	}
}

func TestAdapterClassifiesPermanentAndTransientProviderFailuresSafely(t *testing.T) {
	for _, test := range []struct {
		status    int
		class     aiprovider.ErrorClass
		retryable bool
	}{
		{http.StatusUnauthorized, aiprovider.ErrorPermanent, false},
		{http.StatusTooManyRequests, aiprovider.ErrorTransient, true},
		{http.StatusGatewayTimeout, aiprovider.ErrorTimeout, true},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.WriteHeader(test.status)
			_, _ = response.Write([]byte(`{"error":"adapter-canary provider-secret"}`))
		}))
		request := adapterRequest(t)
		sink, events := captureAttemptEvents(t, request)
		err := adapterForServer(t, server).Invoke(request, sink)
		server.Close()
		if err != nil || len(*events) != 1 || (*events)[0].Error.Class != test.class || (*events)[0].Error.Retryable != test.retryable || strings.Contains((*events)[0].Error.Message, "canary") || strings.Contains((*events)[0].Error.Message, "provider-secret") {
			t.Fatalf("status=%d events=%#v err=%v", test.status, *events, err)
		}
	}
}

func TestAdapterRejectsModelDriftAndClassifiesMissingTerminalAsInterrupted(t *testing.T) {
	for _, test := range []struct {
		chunk     string
		code      string
		class     aiprovider.ErrorClass
		retryable bool
	}{
		{`{"model":"other-model","choices":[]}`, "AI_MODEL_IDENTITY_MISMATCH", aiprovider.ErrorPermanent, false},
		{`{"model":"fixture-model","choices":[]}`, "AI_PROVIDER_INTERRUPTED", aiprovider.ErrorInterrupted, true},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = response.Write([]byte("data: " + test.chunk + "\n\n"))
		}))
		request := adapterRequest(t)
		sink, events := captureAttemptEvents(t, request)
		err := adapterForServer(t, server).Invoke(request, sink)
		server.Close()
		if err != nil || len(*events) != 1 || (*events)[0].Error.Code != test.code || (*events)[0].Error.Class != test.class || (*events)[0].Error.Retryable != test.retryable {
			t.Fatalf("chunk=%s events=%#v err=%v", test.chunk, *events, err)
		}
	}
}

func TestAdapterTimeoutAndBestEffortCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/event-stream")
		response.WriteHeader(http.StatusOK)
		if flusher, ok := response.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
	}))
	defer server.Close()

	timeoutRequest := adapterRequest(t)
	timeoutRequest.Timeout = 20 * time.Millisecond
	timeoutSink, timeoutEvents := captureAttemptEvents(t, timeoutRequest)
	if err := adapterForServer(t, server).Invoke(timeoutRequest, timeoutSink); err != nil || len(*timeoutEvents) != 1 || (*timeoutEvents)[0].Error.Class != aiprovider.ErrorTimeout {
		t.Fatalf("timeout events=%#v err=%v", *timeoutEvents, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancelRequest := adapterRequest(t)
	cancelRequest.Cancellation = aiprovider.ContextCancellation{Context: ctx, CancelGeneration: cancelRequest.Manifest.CancelGeneration}
	cancelSink, cancelEvents := captureAttemptEvents(t, cancelRequest)
	cancel()
	if err := adapterForServer(t, server).Invoke(cancelRequest, cancelSink); err != nil || len(*cancelEvents) != 1 || (*cancelEvents)[0].Error.Class != aiprovider.ErrorCanceled {
		t.Fatalf("cancel events=%#v err=%v", *cancelEvents, err)
	}
}

func TestAdapterEnforcesContextAndOutputByteLimits(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		requests++
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = response.Write([]byte("data: {\"model\":\"fixture-model\",\"choices\":[{\"delta\":{\"content\":\"{\\\"value\\\":\\\"this-is-too-large\\\"}\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = response.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	contextRequest := adapterRequest(t)
	contextRequest.Limits.MaxContextBytes = 16
	contextSink, contextEvents := captureAttemptEvents(t, contextRequest)
	if err := adapterForServer(t, server).Invoke(contextRequest, contextSink); err != nil || len(*contextEvents) != 1 || (*contextEvents)[0].Error.Code != "AI_BUDGET_EXCEEDED" || requests != 0 {
		t.Fatalf("context events=%#v requests=%d err=%v", *contextEvents, requests, err)
	}

	outputRequest := adapterRequest(t)
	outputRequest.Limits.MaxOutputBytes = 12
	outputSink, outputEvents := captureAttemptEvents(t, outputRequest)
	if err := adapterForServer(t, server).Invoke(outputRequest, outputSink); err != nil || len(*outputEvents) != 1 || (*outputEvents)[0].Error.Code != "AI_BUDGET_EXCEEDED" || requests != 1 {
		t.Fatalf("output events=%#v requests=%d err=%v", *outputEvents, requests, err)
	}
}
