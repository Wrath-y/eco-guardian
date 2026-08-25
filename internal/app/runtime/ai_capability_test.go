package runtime

import (
	"context"
	"path/filepath"
	"testing"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
)

type capabilityEnvironment map[string]string

func (e capabilityEnvironment) LookupEnv(name string) (string, bool) {
	value, ok := e[name]
	return value, ok
}

type capabilityProbe struct{ calls int }

func (p *capabilityProbe) Probe(context.Context, aiprovider.ProbeRequest) (aiprovider.ProbeObservation, error) {
	p.calls++
	return aiprovider.ProbeObservation{ModelAvailable: true, StructuredOutput: true, ToolCalls: true, Streaming: true}, nil
}

type incompatibleCapabilityProbe struct{}

func (incompatibleCapabilityProbe) Probe(context.Context, aiprovider.ProbeRequest) (aiprovider.ProbeObservation, error) {
	return aiprovider.ProbeObservation{ModelAvailable: true, StructuredOutput: true, Streaming: true}, nil
}

func TestAICapabilityServiceResolvesSettingsCredentialAndProbe(t *testing.T) {
	settingsStore := runtimeconfig.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	settings := runtimeconfig.Default()
	settings.AI.Enabled = true
	settings.AI.Endpoint = "http://127.0.0.1:11434/v1"
	settings.AI.Model = "fixture"
	if err := settingsStore.Save(settings); err != nil {
		t.Fatal(err)
	}
	probe := &capabilityProbe{}
	service := AICapabilityService{
		Settings: settingsStore,
		Credentials: aiprovider.CredentialResolver{Environment: capabilityEnvironment{
			aiprovider.OpenAIAPIKeyEnvironment: "fixture-credential",
		}},
		Prober: probe,
	}
	capability := service.Observe(context.Background())
	if capability.State != aiprovider.CapabilityAvailable || capability.EndpointClassification == nil || *capability.EndpointClassification != aiprovider.EndpointLoopback || probe.calls != 1 {
		t.Fatalf("capability=%#v calls=%d", capability, probe.calls)
	}
	configuration, credential, err := service.Resolve(context.Background())
	if err != nil || configuration.Model != "fixture" || !credential.Present() {
		t.Fatalf("resolved configuration=%#v credential=%v err=%v", configuration, credential, err)
	}
}

func TestAICapabilityServiceMissingCredentialDoesNotProbe(t *testing.T) {
	settingsStore := runtimeconfig.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	settings := runtimeconfig.Default()
	settings.AI.Enabled = true
	settings.AI.Endpoint = "http://127.0.0.1:11434/v1"
	settings.AI.Model = "fixture"
	if err := settingsStore.Save(settings); err != nil {
		t.Fatal(err)
	}
	probe := &capabilityProbe{}
	capability := (AICapabilityService{Settings: settingsStore, Prober: probe}).Observe(context.Background())
	if capability.State != aiprovider.CapabilityUnconfigured || probe.calls != 0 {
		t.Fatalf("capability=%#v calls=%d", capability, probe.calls)
	}
}

func TestAICapabilityServiceReportsIncompatibleToolContract(t *testing.T) {
	settingsStore := runtimeconfig.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	settings := runtimeconfig.Default()
	settings.AI.Enabled = true
	settings.AI.Endpoint = "http://127.0.0.1:11434/v1"
	settings.AI.Model = "fixture"
	if err := settingsStore.Save(settings); err != nil {
		t.Fatal(err)
	}
	service := AICapabilityService{
		Settings: settingsStore,
		Credentials: aiprovider.CredentialResolver{Environment: capabilityEnvironment{
			aiprovider.OpenAIAPIKeyEnvironment: "fixture-credential",
		}},
		Prober: incompatibleCapabilityProbe{},
	}
	capability := service.Observe(context.Background())
	if capability.State != aiprovider.CapabilityUnavailable || len(capability.Reasons) != 1 || capability.Reasons[0] != aiprovider.ReasonToolCallsUnsupported {
		t.Fatalf("incompatible capability=%#v", capability)
	}
}
