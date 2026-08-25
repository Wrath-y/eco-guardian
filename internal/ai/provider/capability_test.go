package provider

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

type probeStub struct {
	observation ProbeObservation
	err         error
	calls       int
}

func TestCapabilityOutcomeFixture(t *testing.T) {
	raw, err := os.ReadFile("../../../api/fixtures/ai-provider-capabilities.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Name             string          `json:"name"`
		State            CapabilityState `json:"state"`
		Enabled          bool            `json:"enabled"`
		StructuredOutput bool            `json:"structured_output"`
		ToolCalls        bool            `json:"tool_calls"`
		Streaming        bool            `json:"streaming"`
		Reasons          []string        `json:"reasons"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		configuration := Configuration{Enabled: fixture.Enabled, Endpoint: "http://127.0.0.1:11434/v1", Model: "fixture", Timeout: time.Second}
		secret := Secret{value: []byte("fixture"), source: CredentialManager}
		probe := &probeStub{observation: ProbeObservation{
			ModelAvailable: true, StructuredOutput: fixture.StructuredOutput,
			ToolCalls: fixture.ToolCalls, Streaming: fixture.Streaming,
		}}
		if fixture.Name == "missing-credential" {
			secret = Secret{}
		}
		got := ResolveCapability(context.Background(), configuration, secret, probe)
		if got.State != fixture.State || !reflect.DeepEqual(got.Reasons, fixture.Reasons) {
			t.Errorf("fixture %s capability=%#v", fixture.Name, got)
		}
	}
}

func (p *probeStub) Probe(context.Context, ProbeRequest) (ProbeObservation, error) {
	p.calls++
	return p.observation, p.err
}

func TestValidateProviderEndpointPolicy(t *testing.T) {
	for _, test := range []struct {
		endpoint   string
		allowCloud bool
		want       EndpointClassification
		wantErr    error
	}{
		{"http://127.0.0.1:11434/v1", false, EndpointLoopback, nil},
		{"https://[::1]:8443/v1", false, EndpointLoopback, nil},
		{"http://localhost/v1", false, EndpointLoopback, nil},
		{"https://api.example.com/v1", true, EndpointCloud, nil},
		{"https://api.example.com/v1", false, "", ErrCloudNotAllowed},
		{"http://api.example.com/v1", true, "", ErrEndpointInvalid},
		{"https://10.0.0.1/v1", true, "", ErrEndpointInvalid},
		{"https://user:secret@example.com/v1", true, "", ErrEndpointInvalid},
		{"file:///tmp/provider", true, "", ErrEndpointInvalid},
	} {
		got, err := ValidateEndpoint(test.endpoint, test.allowCloud)
		if got != test.want || !errors.Is(err, test.wantErr) {
			t.Errorf("ValidateEndpoint(%q)=%q,%v want %q,%v", test.endpoint, got, err, test.want, test.wantErr)
		}
	}
}

func TestResolveCapabilityStableStatesAndDiagnostics(t *testing.T) {
	credential := Secret{value: []byte("fixture"), source: CredentialManager}
	base := Configuration{Enabled: true, Endpoint: "http://127.0.0.1:11434/v1", Model: "fixture", Timeout: time.Second}
	for _, test := range []struct {
		name    string
		config  Configuration
		secret  Secret
		probe   *probeStub
		state   CapabilityState
		reasons []string
	}{
		{"disabled", Configuration{}, Secret{}, &probeStub{}, CapabilityUnconfigured, []string{ReasonDisabled}},
		{"missing", Configuration{Enabled: true}, Secret{}, &probeStub{}, CapabilityUnconfigured, []string{ReasonEndpointRequired, ReasonModelRequired, ReasonCredentialRequired}},
		{"unreachable", base, credential, &probeStub{err: ErrCapabilityProbe}, CapabilityUnavailable, []string{ReasonProviderUnavailable}},
		{"incompatible", base, credential, &probeStub{observation: ProbeObservation{ModelAvailable: true, Streaming: true}}, CapabilityUnavailable, []string{ReasonStructuredOutputUnsupported, ReasonToolCallsUnsupported}},
		{"degraded", base, credential, &probeStub{observation: ProbeObservation{ModelAvailable: true, StructuredOutput: true, ToolCalls: true}}, CapabilityDegraded, []string{ReasonStreamingUnavailable}},
		{"available", base, credential, &probeStub{observation: ProbeObservation{ModelAvailable: true, StructuredOutput: true, ToolCalls: true, Streaming: true}}, CapabilityAvailable, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveCapability(context.Background(), test.config, test.secret, test.probe)
			if got.State != test.state || !reflect.DeepEqual(got.Reasons, test.reasons) {
				t.Fatalf("capability=%#v", got)
			}
			if (test.state == CapabilityUnconfigured) && test.probe.calls != 0 {
				t.Fatal("unconfigured provider was probed")
			}
		})
	}
}
