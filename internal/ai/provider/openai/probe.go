package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

const (
	maxProbeResponseBytes = 1 << 20
	defaultProbeTimeout   = 10 * time.Second
)

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Prober struct {
	Client HTTPClient
}

func (p Prober) Probe(ctx context.Context, request aiprovider.ProbeRequest) (aiprovider.ProbeObservation, error) {
	client := p.Client
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	classification, err := aiprovider.ValidateEndpoint(request.Endpoint, request.Classification == aiprovider.EndpointCloud)
	if err != nil || classification != request.Classification {
		return aiprovider.ProbeObservation{}, aiprovider.ErrCapabilityProbe
	}
	timeout := request.Timeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	probeContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	credential := request.Credential.Reveal()
	defer clear(credential)
	if len(credential) == 0 {
		return aiprovider.ProbeObservation{}, aiprovider.ErrCapabilityProbe
	}
	baseURL := strings.TrimRight(request.Endpoint, "/")
	available, err := p.modelAvailable(probeContext, client, baseURL, request.Model, credential)
	if err != nil {
		return aiprovider.ProbeObservation{}, aiprovider.ErrCapabilityProbe
	}
	if !available {
		return aiprovider.ProbeObservation{}, aiprovider.ErrModelUnavailable
	}
	structured, err := p.probeStructuredOutput(probeContext, client, baseURL, request.Model, credential)
	if err != nil {
		return aiprovider.ProbeObservation{}, aiprovider.ErrCapabilityProbe
	}
	tools, err := p.probeToolCalls(probeContext, client, baseURL, request.Model, credential)
	if err != nil {
		return aiprovider.ProbeObservation{}, aiprovider.ErrCapabilityProbe
	}
	streaming, err := p.probeStreaming(probeContext, client, baseURL, request.Model, credential)
	if err != nil {
		return aiprovider.ProbeObservation{}, aiprovider.ErrCapabilityProbe
	}
	return aiprovider.ProbeObservation{
		ModelAvailable: true, StructuredOutput: structured, ToolCalls: tools, Streaming: streaming,
	}, nil
}

func (p Prober) modelAvailable(ctx context.Context, client HTTPClient, baseURL, model string, credential []byte) (bool, error) {
	response, err := p.do(ctx, client, http.MethodGet, baseURL+"/models", nil, credential)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, nil
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := decodeBoundedJSON(response.Body, &result); err != nil {
		return false, err
	}
	for _, candidate := range result.Data {
		if candidate.ID == model {
			return true, nil
		}
	}
	return false, nil
}

func (p Prober) probeStructuredOutput(ctx context.Context, client HTTPClient, baseURL, model string, credential []byte) (bool, error) {
	body := map[string]any{
		"model": model, "max_tokens": 16, "stream": false,
		"messages": []map[string]string{{"role": "user", "content": "Return {\"ok\":true}."}},
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{"name": "eco_capability_probe", "strict": true, "schema": map[string]any{
				"type": "object", "additionalProperties": false, "required": []string{"ok"}, "properties": map[string]any{"ok": map[string]string{"type": "boolean"}},
			}},
		},
	}
	response, err := p.postJSON(ctx, client, baseURL+"/chat/completions", body, credential)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, nil
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := decodeBoundedJSON(response.Body, &result); err != nil || len(result.Choices) != 1 {
		return false, nil
	}
	var content struct {
		OK bool `json:"ok"`
	}
	return json.Unmarshal([]byte(result.Choices[0].Message.Content), &content) == nil && content.OK, nil
}

func (p Prober) probeToolCalls(ctx context.Context, client HTTPClient, baseURL, model string, credential []byte) (bool, error) {
	body := map[string]any{
		"model": model, "max_tokens": 16, "stream": false,
		"messages": []map[string]string{{"role": "user", "content": "Call the capability probe tool."}},
		"tools": []map[string]any{{"type": "function", "function": map[string]any{
			"name": "eco_capability_probe", "description": "Capability probe", "strict": true,
			"parameters": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{}},
		}}},
		"tool_choice": map[string]any{"type": "function", "function": map[string]string{"name": "eco_capability_probe"}},
	}
	response, err := p.postJSON(ctx, client, baseURL+"/chat/completions", body, credential)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, nil
	}
	var result struct {
		Choices []struct {
			Message struct {
				ToolCalls []struct {
					Type     string `json:"type"`
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := decodeBoundedJSON(response.Body, &result); err != nil || len(result.Choices) != 1 || len(result.Choices[0].Message.ToolCalls) != 1 {
		return false, nil
	}
	call := result.Choices[0].Message.ToolCalls[0]
	return call.Type == "function" && call.Function.Name == "eco_capability_probe", nil
}

func (p Prober) probeStreaming(ctx context.Context, client HTTPClient, baseURL, model string, credential []byte) (bool, error) {
	body := map[string]any{
		"model": model, "max_tokens": 1, "stream": true,
		"messages": []map[string]string{{"role": "user", "content": "Reply OK."}},
	}
	response, err := p.postJSON(ctx, client, baseURL+"/chat/completions", body, credential)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return false, nil
	}
	scanner := bufio.NewScanner(io.LimitReader(response.Body, maxProbeResponseBytes+1))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			return true, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func (p Prober) postJSON(ctx context.Context, client HTTPClient, endpoint string, body any, credential []byte) (*http.Response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return p.do(ctx, client, http.MethodPost, endpoint, encoded, credential)
}

func (Prober) do(ctx context.Context, client HTTPClient, method, endpoint string, body []byte, credential []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+string(credential))
	return client.Do(request)
}

func decodeBoundedJSON(reader io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(reader, maxProbeResponseBytes+1))
	if err != nil || len(data) > maxProbeResponseBytes {
		return errors.New("invalid capability response")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid capability response")
	}
	return nil
}
