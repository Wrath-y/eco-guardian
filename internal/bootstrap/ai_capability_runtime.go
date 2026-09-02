package bootstrap

import (
	"context"
	"fmt"
	"sync"
	"time"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	aiopenai "github.com/zouyi/eco-guardian/internal/ai/provider/openai"
	appruntime "github.com/zouyi/eco-guardian/internal/app/runtime"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
)

// aiCapabilityRuntime single-flights and caches active provider probes. The
// status UI polls frequently, but capability checks perform model requests and
// must not create recurring cost or load. A settings/credential-shape change
// invalidates the cache immediately without hashing or revealing the secret.
type aiCapabilityRuntime struct {
	settings    *runtimeconfig.Store
	credentials aiprovider.CredentialResolver
	ttl         time.Duration

	mu      sync.Mutex
	key     string
	expires time.Time
	value   aiprovider.Capability
}

func (runtime *aiCapabilityRuntime) Observe(ctx context.Context) aiprovider.Capability {
	if runtime == nil {
		return aiprovider.Capability{State: aiprovider.CapabilityUnavailable, Reasons: []string{aiprovider.ReasonSettingsUnavailable}}
	}
	key := runtime.configurationKey(ctx)
	now := time.Now().UTC()
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if key != "" && key == runtime.key && now.Before(runtime.expires) {
		return cloneAICapability(runtime.value)
	}
	service := appruntime.AICapabilityService{Settings: runtime.settings, Credentials: runtime.credentials, Prober: aiopenai.Prober{}}
	value := service.Observe(ctx)
	if ctx.Err() != nil && key != "" && key == runtime.key && runtime.key != "" {
		// A caller deadline is not a provider observation. Preserve the previous
		// known result and let a later explicit/TTL refresh try again.
		return cloneAICapability(runtime.value)
	}
	ttl := runtime.ttl
	if ttl <= 0 {
		ttl = time.Hour
	}
	if value.State != aiprovider.CapabilityAvailable && ttl > time.Minute {
		ttl = time.Minute
	}
	runtime.key, runtime.expires, runtime.value = key, now.Add(ttl), cloneAICapability(value)
	return cloneAICapability(value)
}

func (runtime *aiCapabilityRuntime) Invalidate() {
	if runtime == nil {
		return
	}
	runtime.mu.Lock()
	runtime.expires = time.Time{}
	runtime.mu.Unlock()
}

func (runtime *aiCapabilityRuntime) configurationKey(ctx context.Context) string {
	if runtime.settings == nil {
		return ""
	}
	settings, _, err := runtime.settings.Load()
	if err != nil {
		return ""
	}
	credentialPresent, credentialSource := false, aiprovider.CredentialSource("")
	resolutionContext := context.Background()
	if ctx != nil {
		resolutionContext = context.WithoutCancel(ctx)
	}
	if secret, resolveErr := runtime.credentials.Resolve(resolutionContext, aiprovider.OpenAICompatibleProvider); resolveErr == nil {
		credentialPresent, credentialSource = secret.Present(), secret.Source()
	}
	return fmt.Sprintf("%t\x00%s\x00%s\x00%d\x00%t\x00%t\x00%s", settings.AI.Enabled, settings.AI.Endpoint, settings.AI.Model, settings.AI.RequestTimeoutSeconds, settings.AI.AllowCloud, credentialPresent, credentialSource)
}

func cloneAICapability(value aiprovider.Capability) aiprovider.Capability {
	value.Reasons = append([]string(nil), value.Reasons...)
	if value.EndpointClassification != nil {
		classification := *value.EndpointClassification
		value.EndpointClassification = &classification
	}
	return value
}
