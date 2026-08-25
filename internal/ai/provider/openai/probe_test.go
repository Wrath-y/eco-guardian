package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

type testEnvironment map[string]string

func (e testEnvironment) LookupEnv(name string) (string, bool) {
	value, ok := e[name]
	return value, ok
}

func TestOpenAICompatibleCapabilityProbe(t *testing.T) {
	const credential = "probe-canary"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+credential {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if request.URL.Path == "/v1/models" {
			_ = json.NewEncoder(response).Encode(map[string]any{"data": []map[string]string{{"id": "fixture-model"}}})
			return
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if stream, _ := body["stream"].(bool); stream {
			response.Header().Set("Content-Type", "text/event-stream")
			_, _ = response.Write([]byte("data: {\"choices\":[]}\n\n"))
			return
		}
		response.Header().Set("Content-Type", "application/json")
		if _, ok := body["tools"]; ok {
			_, _ = response.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"eco_capability_probe","arguments":"{}"}}]}}]}`))
			return
		}
		_, _ = response.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
	}))
	defer server.Close()
	resolver := aiprovider.CredentialResolver{Environment: testEnvironment{aiprovider.OpenAIAPIKeyEnvironment: credential}}
	secret, err := resolver.Resolve(context.Background(), aiprovider.OpenAICompatibleProvider)
	if err != nil {
		t.Fatal(err)
	}
	capability := aiprovider.ResolveCapability(context.Background(), aiprovider.Configuration{
		Enabled: true, Endpoint: server.URL + "/v1", Model: "fixture-model", Timeout: time.Second,
	}, secret, Prober{Client: server.Client()})
	if capability.State != aiprovider.CapabilityAvailable || len(capability.Reasons) != 0 {
		t.Fatalf("capability=%#v", capability)
	}
}

func TestProbeClassifiesUnsupportedFeaturesWithoutLeakingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/models" {
			_, _ = response.Write([]byte(`{"data":[{"id":"fixture-model"}]}`))
			return
		}
		response.WriteHeader(http.StatusBadRequest)
		_, _ = response.Write([]byte(`{"error":"request included secret-canary"}`))
	}))
	defer server.Close()
	resolver := aiprovider.CredentialResolver{Environment: testEnvironment{aiprovider.OpenAIAPIKeyEnvironment: "secret-canary"}}
	secret, _ := resolver.Resolve(context.Background(), aiprovider.OpenAICompatibleProvider)
	capability := aiprovider.ResolveCapability(context.Background(), aiprovider.Configuration{
		Enabled: true, Endpoint: server.URL + "/v1", Model: "fixture-model", Timeout: time.Second,
	}, secret, Prober{Client: server.Client()})
	if capability.State != aiprovider.CapabilityUnavailable || strings.Contains(strings.Join(capability.Reasons, ","), "secret-canary") {
		t.Fatalf("unsafe capability=%#v", capability)
	}
}
