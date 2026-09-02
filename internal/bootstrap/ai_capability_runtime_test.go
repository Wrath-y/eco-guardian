package bootstrap

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
)

func TestAICapabilityRuntimeCachesProbeUntilConfigurationChanges(t *testing.T) {
	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			_, _ = writer.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
			return
		}
		switch (posts.Add(1) - 1) % 3 {
		case 0:
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"content":"{\"ok\":true}"}}]}`))
		case 1:
			_, _ = writer.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"eco_capability_probe"}}]}}]}`))
		case 2:
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = writer.Write([]byte("data: {}\n\n"))
		}
	}))
	defer server.Close()

	settingsStore := runtimeconfig.NewStore(t.TempDir() + "/settings.json")
	settings := runtimeconfig.Default()
	settings.AI.Enabled, settings.AI.Endpoint, settings.AI.Model = true, server.URL, "model-a"
	if err := settingsStore.Save(settings); err != nil {
		t.Fatal(err)
	}
	t.Setenv(aiprovider.OpenAIAPIKeyEnvironment, "fixture-secret")
	runtime := &aiCapabilityRuntime{
		settings:    settingsStore,
		credentials: aiprovider.CredentialResolver{Environment: aiprovider.OSEnvironment{}},
		ttl:         time.Hour,
	}
	for attempt := 0; attempt < 2; attempt++ {
		capability := runtime.Observe(context.Background())
		if capability.State != aiprovider.CapabilityAvailable {
			t.Fatalf("attempt=%d capability=%#v", attempt, capability)
		}
	}
	if posts.Load() != 3 {
		t.Fatalf("cached status polling made %d model requests", posts.Load())
	}
	runtime.Invalidate()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if capability := runtime.Observe(canceled); capability.State != aiprovider.CapabilityAvailable {
		t.Fatalf("canceled refresh replaced cached capability: %#v", capability)
	}
	if posts.Load() != 3 {
		t.Fatalf("canceled refresh reached provider: posts=%d", posts.Load())
	}
	if capability := runtime.Observe(context.Background()); capability.State != aiprovider.CapabilityAvailable {
		t.Fatalf("invalidated capability=%#v", capability)
	}
	if posts.Load() != 6 {
		t.Fatalf("explicit invalidation did not re-probe: posts=%d", posts.Load())
	}
	settings.AI.Model = "model-b"
	if err := settingsStore.Save(settings); err != nil {
		t.Fatal(err)
	}
	if capability := runtime.Observe(context.Background()); capability.State != aiprovider.CapabilityAvailable {
		t.Fatalf("changed capability=%#v", capability)
	}
	if posts.Load() != 9 {
		t.Fatalf("configuration change did not re-probe: posts=%d", posts.Load())
	}
}
