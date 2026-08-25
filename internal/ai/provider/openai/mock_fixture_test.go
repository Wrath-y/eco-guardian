package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

type capabilityFixture struct {
	Name             string                     `json:"name"`
	ModelsStatus     int                        `json:"models_status"`
	ModelsDelayMS    int                        `json:"models_delay_ms"`
	ModelPresent     bool                       `json:"model_present"`
	StructuredStatus int                        `json:"structured_status"`
	ToolStatus       int                        `json:"tool_status"`
	StreamingStatus  int                        `json:"streaming_status"`
	TimeoutMS        int                        `json:"timeout_ms"`
	ExpectedState    aiprovider.CapabilityState `json:"expected_state"`
	ExpectedReasons  []string                   `json:"expected_reasons"`
}

type streamFixture struct {
	Name                  string            `json:"name"`
	Chunks                []json.RawMessage `json:"chunks"`
	RawChunks             []string          `json:"raw_chunks"`
	Done                  bool              `json:"done"`
	OversizedContentBytes int               `json:"oversized_content_bytes"`
	MaxOutputBytes        int               `json:"max_output_bytes"`
	DelayMS               int               `json:"delay_ms"`
	TimeoutMS             int               `json:"timeout_ms"`
	CancelAfterMS         int               `json:"cancel_after_ms"`
	IgnoreCancel          bool              `json:"ignore_cancel"`
	ExpectedEvents        []string          `json:"expected_events"`
	ExpectedError         string            `json:"expected_error"`
	ExpectedTotalTokens   int64             `json:"expected_total_tokens"`
}

func TestProviderStreamMockFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/stream-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []streamFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if fixture.DelayMS > 0 {
					timer := time.NewTimer(time.Duration(fixture.DelayMS) * time.Millisecond)
					defer timer.Stop()
					if fixture.IgnoreCancel {
						<-timer.C
					} else {
						select {
						case <-timer.C:
						case <-request.Context().Done():
							return
						}
					}
				}
				response.Header().Set("Content-Type", "text/event-stream")
				for _, chunk := range fixture.Chunks {
					_, _ = response.Write([]byte("data: " + string(chunk) + "\n\n"))
				}
				for _, chunk := range fixture.RawChunks {
					_, _ = response.Write([]byte("data: " + chunk + "\n\n"))
				}
				if fixture.OversizedContentBytes > 0 {
					chunk, _ := json.Marshal(map[string]any{
						"model": "fixture-model", "choices": []any{map[string]any{
							"delta": map[string]any{"content": `{"value":"` + strings.Repeat("x", fixture.OversizedContentBytes) + `"}`}, "finish_reason": "stop",
						}},
					})
					_, _ = response.Write([]byte("data: " + string(chunk) + "\n\n"))
				}
				if fixture.Done {
					_, _ = response.Write([]byte("data: [DONE]\n\n"))
				}
			}))
			defer server.Close()

			request := adapterRequest(t)
			if fixture.TimeoutMS > 0 {
				request.Timeout = time.Duration(fixture.TimeoutMS) * time.Millisecond
			}
			if fixture.MaxOutputBytes > 0 {
				request.Limits.MaxOutputBytes = fixture.MaxOutputBytes
			}
			if fixture.CancelAfterMS > 0 {
				ctx, cancel := context.WithCancel(context.Background())
				request.Cancellation = aiprovider.ContextCancellation{Context: ctx, CancelGeneration: request.Manifest.CancelGeneration}
				timer := time.AfterFunc(time.Duration(fixture.CancelAfterMS)*time.Millisecond, cancel)
				defer timer.Stop()
				defer cancel()
			}
			sink, events := captureAttemptEvents(t, request)
			if err := adapterForServer(t, server).Invoke(request, sink); err != nil {
				t.Fatal(err)
			}
			gotTypes := make([]string, len(*events))
			for index, event := range *events {
				gotTypes[index] = string(event.Type)
			}
			if strings.Join(gotTypes, ",") != strings.Join(fixture.ExpectedEvents, ",") {
				t.Fatalf("event types=%v want=%v events=%#v", gotTypes, fixture.ExpectedEvents, *events)
			}
			if fixture.ExpectedError != "" && ((*events)[len(*events)-1].Error == nil || (*events)[len(*events)-1].Error.Code != fixture.ExpectedError) {
				t.Fatalf("error event=%#v want=%s", (*events)[len(*events)-1], fixture.ExpectedError)
			}
			if fixture.ExpectedTotalTokens > 0 && ((*events)[0].Usage == nil || (*events)[0].Usage.TotalTokens != fixture.ExpectedTotalTokens) {
				t.Fatalf("usage events=%#v", *events)
			}
		})
	}
}

func TestCapabilityProbeMockFixtures(t *testing.T) {
	raw, err := os.ReadFile("testdata/capability-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []capabilityFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/v1/models" {
					if fixture.ModelsDelayMS > 0 {
						timer := time.NewTimer(time.Duration(fixture.ModelsDelayMS) * time.Millisecond)
						defer timer.Stop()
						select {
						case <-timer.C:
						case <-request.Context().Done():
							return
						}
					}
					status := fixture.ModelsStatus
					if status == 0 {
						status = http.StatusOK
					}
					response.WriteHeader(status)
					if status >= 200 && status < 300 {
						models := []map[string]string{}
						if fixture.ModelPresent {
							models = append(models, map[string]string{"id": "fixture-model"})
						}
						_ = json.NewEncoder(response).Encode(map[string]any{"data": models})
					}
					return
				}
				var body map[string]any
				_ = json.NewDecoder(request.Body).Decode(&body)
				if stream, _ := body["stream"].(bool); stream {
					if fixture.StreamingStatus >= 200 && fixture.StreamingStatus < 300 {
						response.Header().Set("Content-Type", "text/event-stream")
					}
					response.WriteHeader(fixture.StreamingStatus)
					if fixture.StreamingStatus >= 200 && fixture.StreamingStatus < 300 {
						_, _ = response.Write([]byte("data: {}\n\n"))
					}
					return
				}
				if _, tools := body["tools"]; tools {
					response.WriteHeader(fixture.ToolStatus)
					if fixture.ToolStatus >= 200 && fixture.ToolStatus < 300 {
						_, _ = response.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"eco_capability_probe"}}]}}]}`))
					}
					return
				}
				response.WriteHeader(fixture.StructuredStatus)
				if fixture.StructuredStatus >= 200 && fixture.StructuredStatus < 300 {
					_, _ = response.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
				}
			}))
			defer server.Close()
			resolver := aiprovider.CredentialResolver{Environment: testEnvironment{aiprovider.OpenAIAPIKeyEnvironment: "fixture-secret"}}
			secret, err := resolver.Resolve(context.Background(), aiprovider.OpenAICompatibleProvider)
			if err != nil {
				t.Fatal(err)
			}
			timeout := time.Second
			if fixture.TimeoutMS > 0 {
				timeout = time.Duration(fixture.TimeoutMS) * time.Millisecond
			}
			capability := aiprovider.ResolveCapability(context.Background(), aiprovider.Configuration{
				Enabled: true, Endpoint: server.URL + "/v1", Model: "fixture-model", Timeout: timeout,
			}, secret, Prober{Client: server.Client()})
			if capability.State != fixture.ExpectedState || strings.Join(capability.Reasons, ",") != strings.Join(fixture.ExpectedReasons, ",") {
				t.Fatalf("capability=%#v want state=%s reasons=%v", capability, fixture.ExpectedState, fixture.ExpectedReasons)
			}
		})
	}
}
