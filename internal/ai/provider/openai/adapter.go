package openai

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

const wireEnvelopeBytes = 4096

var (
	errModelIdentityMismatch = errors.New("provider model identity mismatch")
	errProviderBudget        = errors.New("provider payload budget exceeded")
)

type AdapterConfig struct {
	Endpoint       string
	Classification aiprovider.EndpointClassification
	Model          string
	Credential     aiprovider.Secret
}

type Adapter struct {
	config AdapterConfig
	client HTTPClient
}

func NewAdapter(config AdapterConfig, client HTTPClient) (*Adapter, error) {
	classification, err := aiprovider.ValidateEndpoint(config.Endpoint, config.Classification == aiprovider.EndpointCloud)
	if err != nil || classification != config.Classification || strings.TrimSpace(config.Model) == "" || !config.Credential.Present() {
		return nil, aiprovider.ErrAttemptInvalid
	}
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Adapter{config: config, client: client}, nil
}

func (a *Adapter) Invoke(request aiprovider.AttemptRequest, sink aiprovider.EventSink) error {
	if a == nil || !request.Valid() || sink == nil {
		return aiprovider.ErrAttemptInvalid
	}
	request = request.Clone()
	sequence := int64(1)
	if request.Manifest.EndpointClassification != a.config.Classification || request.Manifest.Model.ID != a.config.Model {
		return emitFailure(sink, &sequence, "AI_PROVIDER_CONFIGURATION_INVALID", aiprovider.ErrorPermanent, false, "Provider configuration does not match the attempt manifest")
	}
	body, err := buildChatRequest(request, a.config.Model)
	if err != nil {
		return aiprovider.ErrAttemptInvalid
	}
	if len(body) > request.Limits.MaxContextBytes {
		return emitFailure(sink, &sequence, "AI_BUDGET_EXCEEDED", aiprovider.ErrorPermanent, false, "Provider context exceeded the configured byte limit")
	}
	ctx, cancel := invocationContext(request.Cancellation, request.Timeout)
	defer cancel()
	credential := a.config.Credential.Reveal()
	defer clear(credential)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(a.config.Endpoint, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return aiprovider.ErrAttemptInvalid
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	httpRequest.Header.Set("Authorization", "Bearer "+string(credential))
	response, err := a.client.Do(httpRequest)
	if err != nil {
		code, class, retryable, message := classifyTransportError(ctx, request.Cancellation)
		return emitFailure(sink, &sequence, code, class, retryable, message)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		code, class, retryable, message := classifyStatus(response.StatusCode)
		return emitFailureWithRequestID(sink, &sequence, code, class, retryable, message, aiprovider.SafeRequestID(response.Header.Get("X-Request-ID")))
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return emitFailure(sink, &sequence, "AI_PROVIDER_STREAM_UNSUPPORTED", aiprovider.ErrorPermanent, false, "Provider did not return an event stream")
	}
	return a.consumeStream(ctx, response.Body, request, sink, &sequence)
}

func buildChatRequest(request aiprovider.AttemptRequest, model string) ([]byte, error) {
	userPayload, err := json.Marshal(map[string]json.RawMessage{
		"input": request.UserInput, "evidence": request.EvidenceSummary,
	})
	if err != nil {
		return nil, err
	}
	messages := []map[string]any{
		{"role": "system", "content": request.Prompt.System},
		{"role": "developer", "content": request.Prompt.Developer},
		{"role": "user", "content": string(userPayload)},
	}
	if len(request.ToolResults) != 0 {
		calls := make([]map[string]any, 0, len(request.ToolResults))
		for _, result := range request.ToolResults {
			calls = append(calls, map[string]any{
				"id": result.CallID, "type": "function",
				"function": map[string]any{"name": result.Tool.ID, "arguments": string(result.Arguments)},
			})
		}
		messages = append(messages, map[string]any{"role": "assistant", "tool_calls": calls})
		for _, result := range request.ToolResults {
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": result.CallID, "content": string(result.Result)})
		}
	}
	tools := make([]map[string]any, 0, len(request.Tools))
	for _, tool := range request.Tools {
		tools = append(tools, map[string]any{
			"type": "function", "function": map[string]any{
				"name": tool.Identity.ID, "description": tool.Description, "strict": true, "parameters": tool.InputSchema,
			},
		})
	}
	payload := map[string]any{
		"model": model, "messages": messages, "stream": true,
		"stream_options": map[string]bool{"include_usage": true},
		"temperature":    json.Number(request.Parameters.Temperature), "top_p": json.Number(request.Parameters.TopP),
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": request.Manifest.StructuredResponseSchema.ID, "strict": true, "schema": request.ResponseSchema,
		}},
		"tools": tools,
	}
	if request.Parameters.Seed != nil {
		payload["seed"] = *request.Parameters.Seed
	}
	return json.Marshal(payload)
}

type streamToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

type streamAccumulator struct {
	content      strings.Builder
	tools        map[int]*streamToolCall
	finishReason string
	modelSeen    string
}

func (a *Adapter) consumeStream(ctx context.Context, reader io.Reader, request aiprovider.AttemptRequest, sink aiprovider.EventSink, sequence *int64) error {
	accumulator := streamAccumulator{tools: map[int]*streamToolCall{}}
	scanner := bufio.NewScanner(reader)
	maxEventBytes := request.Limits.MaxOutputBytes
	if request.Limits.MaxToolResultBytes > maxEventBytes {
		maxEventBytes = request.Limits.MaxToolResultBytes
	}
	maxEventBytes += wireEnvelopeBytes
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			return emitFailure(sink, sequence, "AI_PROVIDER_STREAM_INVALID", aiprovider.ErrorPermanent, false, "Provider event stream is invalid")
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return finalizeStream(accumulator, request, sink, sequence)
		}
		if err := consumeChunk([]byte(data), request, &accumulator, sink, sequence); err != nil {
			if errors.Is(err, aiprovider.ErrEventInvalid) || errors.Is(err, aiprovider.ErrEventOrder) || errors.Is(err, aiprovider.ErrTerminalConflict) {
				return err
			}
			if errors.Is(err, errModelIdentityMismatch) {
				return emitFailure(sink, sequence, "AI_MODEL_IDENTITY_MISMATCH", aiprovider.ErrorPermanent, false, "Provider model identity did not match the attempt manifest")
			}
			if errors.Is(err, errProviderBudget) {
				return emitFailure(sink, sequence, "AI_BUDGET_EXCEEDED", aiprovider.ErrorPermanent, false, "Provider output exceeded the configured byte limit")
			}
			return emitFailure(sink, sequence, "AI_PROVIDER_STREAM_INVALID", aiprovider.ErrorPermanent, false, "Provider event stream is invalid")
		}
	}
	if ctx.Err() != nil {
		code, class, retryable, message := classifyTransportError(ctx, request.Cancellation)
		return emitFailure(sink, sequence, code, class, retryable, message)
	}
	if scanner.Err() != nil {
		return emitFailure(sink, sequence, "AI_BUDGET_EXCEEDED", aiprovider.ErrorPermanent, false, "Provider event exceeded the configured byte limit")
	}
	return emitFailure(sink, sequence, "AI_PROVIDER_INTERRUPTED", aiprovider.ErrorInterrupted, true, "Provider stream ended without a terminal event")
}

func consumeChunk(data []byte, request aiprovider.AttemptRequest, accumulator *streamAccumulator, sink aiprovider.EventSink, sequence *int64) error {
	var chunk struct {
		Model   string `json:"model"`
		Choices []struct {
			Delta struct {
				Content   *string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *aiprovider.Usage `json:"usage"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return err
	}
	if chunk.Model != "" {
		if chunk.Model != request.Manifest.Model.ID {
			return errModelIdentityMismatch
		}
		accumulator.modelSeen = chunk.Model
	}
	if len(chunk.Choices) > 1 {
		return errors.New("multiple choices")
	}
	if len(chunk.Choices) == 1 {
		choice := chunk.Choices[0]
		if choice.Delta.Content != nil {
			if accumulator.content.Len()+len(*choice.Delta.Content) > request.Limits.MaxOutputBytes {
				return errProviderBudget
			}
			accumulator.content.WriteString(*choice.Delta.Content)
		}
		for _, delta := range choice.Delta.ToolCalls {
			call := accumulator.tools[delta.Index]
			if call == nil {
				call = &streamToolCall{}
				accumulator.tools[delta.Index] = call
			}
			if delta.ID != "" {
				if call.ID != "" && call.ID != delta.ID {
					return errors.New("tool call identity changed")
				}
				call.ID = delta.ID
			}
			if delta.Function.Name != "" {
				if call.Name != "" && call.Name != delta.Function.Name {
					return errors.New("tool name changed")
				}
				call.Name = delta.Function.Name
			}
			if call.Arguments.Len()+len(delta.Function.Arguments) > request.Limits.MaxToolResultBytes {
				return errProviderBudget
			}
			call.Arguments.WriteString(delta.Function.Arguments)
		}
		if choice.FinishReason != nil {
			accumulator.finishReason = *choice.FinishReason
		}
	}
	if chunk.Usage != nil {
		if !chunk.Usage.Valid() {
			return errors.New("invalid usage")
		}
		if err := sink.Emit(aiprovider.Event{Sequence: *sequence, Type: aiprovider.EventUsage, Usage: chunk.Usage}); err != nil {
			return err
		}
		*sequence++
	}
	return nil
}

func finalizeStream(accumulator streamAccumulator, request aiprovider.AttemptRequest, sink aiprovider.EventSink, sequence *int64) error {
	if accumulator.modelSeen == "" {
		return emitFailure(sink, sequence, "AI_MODEL_IDENTITY_MISMATCH", aiprovider.ErrorPermanent, false, "Provider response omitted model identity")
	}
	if accumulator.finishReason == "length" {
		return emitFailure(sink, sequence, "AI_OUTPUT_TRUNCATED", aiprovider.ErrorPermanent, false, "Provider structured response was truncated")
	}
	if len(accumulator.tools) != 0 {
		if strings.TrimSpace(accumulator.content.String()) != "" || accumulator.finishReason != "tool_calls" {
			return emitFailure(sink, sequence, "AI_PROVIDER_STREAM_INVALID", aiprovider.ErrorPermanent, false, "Provider mixed tool calls with structured output")
		}
		indices := make([]int, 0, len(accumulator.tools))
		for index := range accumulator.tools {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		for _, index := range indices {
			call := accumulator.tools[index]
			arguments := json.RawMessage(call.Arguments.String())
			event := aiprovider.Event{Sequence: *sequence, Type: aiprovider.EventToolCall, ToolCall: &aiprovider.ToolCall{
				CallID: aicontract.ToolCallID(call.ID), Tool: toolIdentity(request.Manifest.Tools, call.Name), Arguments: arguments,
			}}
			if !event.Valid() {
				return emitFailure(sink, sequence, "AI_TOOL_CALL_INVALID", aiprovider.ErrorPermanent, false, "Provider tool call is invalid")
			}
			if err := sink.Emit(event); err != nil {
				return err
			}
			*sequence++
		}
		return nil
	}
	body := json.RawMessage(accumulator.content.String())
	if len(body) > request.Limits.MaxOutputBytes {
		return emitFailure(sink, sequence, "AI_BUDGET_EXCEEDED", aiprovider.ErrorPermanent, false, "Provider output exceeded the configured byte limit")
	}
	if accumulator.finishReason != "stop" || !json.Valid(body) {
		return emitFailure(sink, sequence, "AI_OUTPUT_INVALID", aiprovider.ErrorPermanent, false, "Provider structured response is invalid")
	}
	hash := sha256.Sum256(body)
	response := &aiprovider.StructuredResponse{
		Schema: request.Manifest.StructuredResponseSchema, Body: append(json.RawMessage(nil), body...), BodyHash: aicontract.Hash(hex.EncodeToString(hash[:])),
	}
	if err := sink.Emit(aiprovider.Event{Sequence: *sequence, Type: aiprovider.EventStructuredResponse, StructuredResponse: response}); err != nil {
		return err
	}
	*sequence++
	return nil
}

func toolIdentity(tools []aicontract.VersionIdentity, name string) aicontract.VersionIdentity {
	for _, tool := range tools {
		if tool.ID == name {
			return tool
		}
	}
	return aicontract.VersionIdentity{}
}

func invocationContext(token aiprovider.CancellationToken, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	go func() {
		select {
		case <-token.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func classifyTransportError(ctx context.Context, token aiprovider.CancellationToken) (string, aiprovider.ErrorClass, bool, string) {
	if token.Err() != nil {
		return "AI_CANCELED", aiprovider.ErrorCanceled, false, "Provider request was canceled"
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "AI_PROVIDER_TIMEOUT", aiprovider.ErrorTimeout, true, "Provider request timed out"
	}
	return "AI_PROVIDER_FAILED", aiprovider.ErrorTransient, true, "Provider dependency failed"
}

func classifyStatus(status int) (string, aiprovider.ErrorClass, bool, string) {
	switch {
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return "AI_PROVIDER_TIMEOUT", aiprovider.ErrorTimeout, true, "Provider request timed out"
	case status == http.StatusTooManyRequests || status >= 500:
		return "AI_PROVIDER_FAILED", aiprovider.ErrorTransient, true, "Provider dependency failed"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "AI_PROVIDER_AUTH_FAILED", aiprovider.ErrorPermanent, false, "Provider credential was rejected"
	case status == http.StatusNotFound:
		return "AI_MODEL_UNAVAILABLE", aiprovider.ErrorPermanent, false, "Provider model is unavailable"
	default:
		return "AI_PROVIDER_REQUEST_REJECTED", aiprovider.ErrorPermanent, false, "Provider rejected the request"
	}
}

func emitFailure(sink aiprovider.EventSink, sequence *int64, code string, class aiprovider.ErrorClass, retryable bool, message string) error {
	return emitFailureWithRequestID(sink, sequence, code, class, retryable, message, "")
}

func emitFailureWithRequestID(sink aiprovider.EventSink, sequence *int64, code string, class aiprovider.ErrorClass, retryable bool, message, requestID string) error {
	event := aiprovider.Event{Sequence: *sequence, Type: aiprovider.EventError, Error: &aiprovider.TerminalError{
		Code: code, Class: class, Retryable: retryable, Message: message, RequestID: requestID,
	}}
	if err := sink.Emit(event); err != nil {
		return err
	}
	*sequence++
	return nil
}
