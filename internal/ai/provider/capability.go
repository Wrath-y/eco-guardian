package provider

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type EndpointClassification string

const (
	EndpointLoopback EndpointClassification = "loopback"
	EndpointCloud    EndpointClassification = "cloud"
)

var (
	ErrEndpointInvalid  = errors.New("AI Provider endpoint is invalid")
	ErrCloudNotAllowed  = errors.New("cloud AI Provider is not allowed")
	ErrCapabilityProbe  = errors.New("AI Provider capability probe failed")
	ErrModelUnavailable = errors.New("AI Provider model is unavailable")
)

type CapabilityState string

const (
	CapabilityUnconfigured CapabilityState = "unconfigured"
	CapabilityAvailable    CapabilityState = "available"
	CapabilityDegraded     CapabilityState = "degraded"
	CapabilityUnavailable  CapabilityState = "unavailable"
)

const (
	ReasonProviderUnconfigured        = "AI_PROVIDER_UNCONFIGURED"
	ReasonDisabled                    = "AI_DISABLED"
	ReasonEndpointRequired            = "AI_ENDPOINT_REQUIRED"
	ReasonModelRequired               = "AI_MODEL_REQUIRED"
	ReasonCredentialRequired          = "AI_CREDENTIAL_REQUIRED"
	ReasonCredentialUnavailable       = "AI_CREDENTIAL_UNAVAILABLE"
	ReasonSettingsUnavailable         = "AI_SETTINGS_UNAVAILABLE"
	ReasonEndpointInvalid             = "AI_ENDPOINT_INVALID"
	ReasonCloudNotAllowed             = "AI_CLOUD_NOT_ALLOWED"
	ReasonProviderUnavailable         = "AI_PROVIDER_UNAVAILABLE"
	ReasonModelUnavailable            = "AI_MODEL_UNAVAILABLE"
	ReasonStructuredOutputUnsupported = "AI_STRUCTURED_OUTPUT_UNSUPPORTED"
	ReasonToolCallsUnsupported        = "AI_TOOL_CALLS_UNSUPPORTED"
	ReasonStreamingUnavailable        = "AI_STREAMING_UNAVAILABLE"
)

type Configuration struct {
	Enabled    bool
	Endpoint   string
	Model      string
	Timeout    time.Duration
	AllowCloud bool
}

type ProbeRequest struct {
	Endpoint       string
	Classification EndpointClassification
	Model          string
	Credential     Secret
	Timeout        time.Duration
}

type ProbeObservation struct {
	ModelAvailable   bool
	StructuredOutput bool
	ToolCalls        bool
	Streaming        bool
}

type CapabilityProber interface {
	Probe(context.Context, ProbeRequest) (ProbeObservation, error)
}

type Capability struct {
	State                  CapabilityState
	Enabled                bool
	EndpointClassification *EndpointClassification
	CredentialPresent      bool
	StructuredOutput       bool
	ToolCalls              bool
	Streaming              bool
	Reasons                []string
}

// ResolveCapability performs configuration validation before any network
// request and maps probe outcomes to stable, safe diagnostics.
func ResolveCapability(ctx context.Context, configuration Configuration, credential Secret, prober CapabilityProber) Capability {
	result := Capability{State: CapabilityUnconfigured, Enabled: configuration.Enabled, CredentialPresent: credential.Present()}
	if !configuration.Enabled {
		result.Reasons = []string{ReasonDisabled}
		return result
	}
	if strings.TrimSpace(configuration.Endpoint) == "" {
		result.Reasons = append(result.Reasons, ReasonEndpointRequired)
	}
	if strings.TrimSpace(configuration.Model) == "" {
		result.Reasons = append(result.Reasons, ReasonModelRequired)
	}
	if !credential.Present() {
		result.Reasons = append(result.Reasons, ReasonCredentialRequired)
	}
	if len(result.Reasons) != 0 {
		return result
	}
	classification, err := ValidateEndpoint(configuration.Endpoint, configuration.AllowCloud)
	if err != nil {
		result.State = CapabilityUnavailable
		if errors.Is(err, ErrCloudNotAllowed) {
			result.Reasons = []string{ReasonCloudNotAllowed}
		} else {
			result.Reasons = []string{ReasonEndpointInvalid}
		}
		return result
	}
	result.EndpointClassification = &classification
	if prober == nil {
		result.State = CapabilityUnavailable
		result.Reasons = []string{ReasonProviderUnavailable}
		return result
	}
	observation, err := prober.Probe(ctx, ProbeRequest{
		Endpoint: configuration.Endpoint, Classification: classification,
		Model: configuration.Model, Credential: credential, Timeout: configuration.Timeout,
	})
	if err != nil {
		result.State = CapabilityUnavailable
		if errors.Is(err, ErrModelUnavailable) {
			result.Reasons = []string{ReasonModelUnavailable}
		} else {
			result.Reasons = []string{ReasonProviderUnavailable}
		}
		return result
	}
	result.StructuredOutput = observation.StructuredOutput
	result.ToolCalls = observation.ToolCalls
	result.Streaming = observation.Streaming
	if !observation.ModelAvailable {
		result.Reasons = append(result.Reasons, ReasonModelUnavailable)
	}
	if !observation.StructuredOutput {
		result.Reasons = append(result.Reasons, ReasonStructuredOutputUnsupported)
	}
	if !observation.ToolCalls {
		result.Reasons = append(result.Reasons, ReasonToolCallsUnsupported)
	}
	if len(result.Reasons) != 0 {
		result.State = CapabilityUnavailable
		return result
	}
	if !observation.Streaming {
		result.State = CapabilityDegraded
		result.Reasons = []string{ReasonStreamingUnavailable}
		return result
	}
	result.State = CapabilityAvailable
	result.Reasons = []string{}
	return result
}

// ValidateEndpoint accepts only unauthenticated loopback HTTP(S), or an
// explicitly disclosed non-loopback HTTPS endpoint. Private and link-local IP
// literals are not cloud endpoints.
func ValidateEndpoint(value string, allowCloud bool) (EndpointClassification, error) {
	if value == "" || len(value) > 2048 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return "", ErrEndpointInvalid
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrEndpointInvalid
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", ErrEndpointInvalid
	}
	if port := parsed.Port(); port != "" {
		portNumber, convertErr := strconv.Atoi(port)
		if convertErr != nil || portNumber < 1 || portNumber > 65535 {
			return "", ErrEndpointInvalid
		}
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return EndpointLoopback, nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return EndpointLoopback, nil
		}
		if ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return "", ErrEndpointInvalid
		}
	}
	if parsed.Scheme != "https" {
		return "", ErrEndpointInvalid
	}
	if !allowCloud {
		return "", ErrCloudNotAllowed
	}
	return EndpointCloud, nil
}
