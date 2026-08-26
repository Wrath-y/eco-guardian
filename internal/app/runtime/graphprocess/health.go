package graphprocess

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	graphclient "github.com/zouyi/eco-guardian/internal/graph/client"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

type RootRequestIDs interface {
	NewRootRequestID() string
}

type CryptoRequestIDs struct{}

func (CryptoRequestIDs) NewRootRequestID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "eco-health-unavailable"
	}
	return "eco-health-" + hex.EncodeToString(value[:])
}

type HealthProbeFailure struct {
	Code      string
	RequestID string
	Retryable bool
}

func (failure HealthProbeFailure) Error() string {
	if failure.Code == "" {
		return "local-rag health probe failed"
	}
	return "local-rag health probe failed: " + failure.Code
}

type ClientHealthAdapter struct {
	HTTPClient   *http.Client
	RequestIDs   RootRequestIDs
	PollInterval time.Duration
}

// ProbeCompatibility uses the existing Graph client for loopback validation,
// X-Request-ID propagation, typed provider errors, and health decoding.
func (adapter ClientHealthAdapter) ProbeCompatibility(ctx context.Context, endpoint string) (CompatibilityObservation, error) {
	compatibility, err := adapter.ObserveHealth(ctx, endpoint)
	if err != nil {
		return CompatibilityObservation{}, err
	}
	return CompatibilityObservation{Compatible: compatibility.Compatible, Reasons: append([]string(nil), compatibility.Reasons...)}, nil
}

func (adapter ClientHealthAdapter) ObserveHealth(ctx context.Context, endpoint string) (HealthCompatibility, error) {
	client, err := graphclient.New(graphclient.Config{Endpoint: endpoint, HTTPClient: adapter.HTTPClient})
	if err != nil {
		return HealthCompatibility{}, HealthProbeFailure{Code: "ENDPOINT_INVALID"}
	}
	requestIDs := adapter.RequestIDs
	if requestIDs == nil {
		requestIDs = CryptoRequestIDs{}
	}
	requestID := requestIDs.NewRootRequestID()
	health, err := client.Health(ctx, requestID)
	if err != nil {
		var provider *graphsync.ProviderError
		if errors.As(err, &provider) {
			safe := graphclient.SafeProviderError(provider)
			return HealthCompatibility{}, HealthProbeFailure{Code: safe.Code, RequestID: safe.RequestID, Retryable: safe.Retryable}
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return HealthCompatibility{}, err
		}
		if errors.Is(err, graphclient.ErrContract) {
			return HealthCompatibility{}, HealthProbeFailure{Code: "HEALTH_CONTRACT_INVALID"}
		}
		return HealthCompatibility{}, HealthProbeFailure{Code: "HEALTH_TRANSPORT_FAILED", Retryable: true}
	}
	return EvaluateHealthCompatibility(health, nil), nil
}

func (adapter ClientHealthAdapter) WaitReady(ctx context.Context, endpoint string, _ platformprocess.LaunchGeneration) error {
	interval := adapter.PollInterval
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	for {
		observation, err := adapter.ProbeCompatibility(ctx, endpoint)
		if err == nil && observation.Compatible {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		}
	}
}

var _ CompatibilityProber = ClientHealthAdapter{}
var _ Readiness = ClientHealthAdapter{}
