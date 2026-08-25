package runtime

import (
	"context"
	"errors"
	"time"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
)

var ErrAIProviderConfiguration = errors.New("AI Provider configuration is unavailable")

type AISettingsLoader interface {
	Load() (runtimeconfig.Settings, bool, error)
}

// AICapabilityService resolves machine settings and credential presence for a
// single explicit probe. It returns safe diagnostics instead of errors so an
// optional AI dependency cannot fail unrelated runtime capabilities.
type AICapabilityService struct {
	Settings    AISettingsLoader
	Credentials aiprovider.CredentialResolver
	Prober      aiprovider.CapabilityProber
}

// Resolve snapshots the non-sensitive settings and secret handle exactly once
// for an attempt. It satisfies the AI orchestration configuration port without
// importing orchestration or a concrete Provider adapter.
func (s AICapabilityService) Resolve(ctx context.Context) (aiprovider.Configuration, aiprovider.Secret, error) {
	if s.Settings == nil {
		return aiprovider.Configuration{}, aiprovider.Secret{}, ErrAIProviderConfiguration
	}
	settings, _, err := s.Settings.Load()
	if err != nil || !settings.AI.Enabled {
		return aiprovider.Configuration{}, aiprovider.Secret{}, ErrAIProviderConfiguration
	}
	configuration := aiprovider.Configuration{
		Enabled: true, Endpoint: settings.AI.Endpoint, Model: settings.AI.Model,
		Timeout: time.Duration(settings.AI.RequestTimeoutSeconds) * time.Second, AllowCloud: settings.AI.AllowCloud,
	}
	if _, err := aiprovider.ValidateEndpoint(configuration.Endpoint, configuration.AllowCloud); err != nil {
		return aiprovider.Configuration{}, aiprovider.Secret{}, ErrAIProviderConfiguration
	}
	secret, err := s.Credentials.Resolve(ctx, aiprovider.OpenAICompatibleProvider)
	if err != nil {
		return aiprovider.Configuration{}, aiprovider.Secret{}, ErrAIProviderConfiguration
	}
	return configuration, secret, nil
}

func (s AICapabilityService) Observe(ctx context.Context) aiprovider.Capability {
	if s.Settings == nil {
		return aiprovider.Capability{State: aiprovider.CapabilityUnavailable, Reasons: []string{aiprovider.ReasonSettingsUnavailable}}
	}
	settings, _, err := s.Settings.Load()
	if err != nil {
		return aiprovider.Capability{State: aiprovider.CapabilityUnavailable, Reasons: []string{aiprovider.ReasonSettingsUnavailable}}
	}
	configuration := aiprovider.Configuration{
		Enabled: settings.AI.Enabled, Endpoint: settings.AI.Endpoint, Model: settings.AI.Model,
		Timeout: time.Duration(settings.AI.RequestTimeoutSeconds) * time.Second, AllowCloud: settings.AI.AllowCloud,
	}
	secret, err := s.Credentials.Resolve(ctx, aiprovider.OpenAICompatibleProvider)
	if err != nil && !errors.Is(err, aiprovider.ErrCredentialNotFound) {
		capability := aiprovider.Capability{State: aiprovider.CapabilityUnavailable, Enabled: settings.AI.Enabled, Reasons: []string{aiprovider.ReasonCredentialUnavailable}}
		if classification, validationErr := aiprovider.ValidateEndpoint(settings.AI.Endpoint, settings.AI.AllowCloud); validationErr == nil {
			capability.EndpointClassification = &classification
		}
		return capability
	}
	return aiprovider.ResolveCapability(ctx, configuration, secret, s.Prober)
}
