package graphprocess

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

type HealthSource interface {
	ObserveHealth(context.Context, string) (HealthCompatibility, error)
}

type HealthObservationState string

const (
	HealthUnknown     HealthObservationState = "unknown"
	HealthHealthy     HealthObservationState = "healthy"
	HealthDegraded    HealthObservationState = "degraded"
	HealthUnavailable HealthObservationState = "unavailable"
)

type HealthObservation struct {
	Generation    uint64
	State         HealthObservationState
	Compatibility HealthCompatibility
	Reason        string
	RequestID     string
	Retryable     bool
	ObservedAt    time.Time
	ExpiresAt     time.Time
}

type HealthObserverOptions struct {
	Source  HealthSource
	Clock   platformprocess.Clock
	Timeout time.Duration
	TTL     time.Duration
}

type healthProbeCall struct {
	endpoint     string
	invalidation uint64
	done         chan struct{}
	observation  HealthObservation
}

type HealthObserver struct {
	mu           sync.Mutex
	source       HealthSource
	clock        platformprocess.Clock
	timeout      time.Duration
	ttl          time.Duration
	endpoint     string
	cached       HealthObservation
	inflight     *healthProbeCall
	invalidation uint64
}

func NewHealthObserver(options HealthObserverOptions) (*HealthObserver, error) {
	if options.Source == nil || options.Timeout < 10*time.Millisecond || options.Timeout > 60*time.Second || options.TTL < 10*time.Millisecond || options.TTL > time.Minute {
		return nil, ErrSupervisorInvalid
	}
	if options.Clock == nil {
		options.Clock = platformprocess.SystemClock{}
	}
	return &HealthObserver{source: options.Source, clock: options.Clock, timeout: options.Timeout, ttl: options.TTL}, nil
}

func (observer *HealthObserver) Observe(ctx context.Context, endpoint string) (HealthObservation, error) {
	if observer == nil || strings.TrimSpace(endpoint) == "" {
		return HealthObservation{}, ErrSupervisorInvalid
	}
	for {
		observer.mu.Lock()
		now := observer.clock.Now().UTC()
		if observer.endpoint == endpoint && observer.cached.Generation != 0 && now.Before(observer.cached.ExpiresAt) {
			cached := cloneHealthObservation(observer.cached)
			observer.mu.Unlock()
			return cached, nil
		}
		if observer.inflight != nil {
			call := observer.inflight
			observer.mu.Unlock()
			select {
			case <-call.done:
				if call.endpoint == endpoint {
					return cloneHealthObservation(call.observation), nil
				}
				continue
			case <-ctx.Done():
				return HealthObservation{}, ctx.Err()
			}
		}
		call := &healthProbeCall{endpoint: endpoint, invalidation: observer.invalidation, done: make(chan struct{})}
		observer.inflight = call
		observer.mu.Unlock()

		probeContext, cancel := context.WithTimeout(ctx, observer.timeout)
		compatibility, probeErr := observer.source.ObserveHealth(probeContext, endpoint)
		cancel()
		observedAt := observer.clock.Now().UTC()
		observation := HealthObservation{Compatibility: compatibility, State: classifyHealthObservation(compatibility, probeErr), ObservedAt: observedAt, ExpiresAt: observedAt.Add(observer.ttl)}
		if probeErr != nil {
			observation.Reason, observation.RequestID, observation.Retryable = safeHealthFailure(probeErr)
		}
		observer.mu.Lock()
		if call.invalidation != observer.invalidation {
			observation.ExpiresAt = time.Time{}
		}
		observation.Generation = observer.cached.Generation + 1
		observer.endpoint = endpoint
		observer.cached = cloneHealthObservation(observation)
		call.observation = cloneHealthObservation(observation)
		observer.inflight = nil
		close(call.done)
		observer.mu.Unlock()
		return observation, nil
	}
}

// Invalidate is called by process exits/starts, provider outcomes, and explicit
// reconnect. It never performs a probe or any business operation itself.
func (observer *HealthObserver) Invalidate() {
	if observer == nil {
		return
	}
	observer.mu.Lock()
	observer.invalidation++
	observer.cached.ExpiresAt = time.Time{}
	observer.mu.Unlock()
}

func (observer *HealthObserver) Snapshot() HealthObservation {
	if observer == nil {
		return HealthObservation{State: HealthUnknown}
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return cloneHealthObservation(observer.cached)
}

func classifyHealthObservation(compatibility HealthCompatibility, err error) HealthObservationState {
	if err != nil || !compatibility.Compatible {
		return HealthUnavailable
	}
	if compatibility.Status == "degraded" {
		return HealthDegraded
	}
	for _, operation := range compatibility.Operations {
		if operation.State != OperationAvailable {
			return HealthDegraded
		}
	}
	return HealthHealthy
}

func safeHealthFailure(err error) (string, string, bool) {
	if errors.Is(err, context.DeadlineExceeded) {
		return "HEALTH_TIMEOUT", "", true
	}
	if errors.Is(err, context.Canceled) {
		return "HEALTH_CANCELED", "", true
	}
	var failure HealthProbeFailure
	if errors.As(err, &failure) {
		return failure.Code, failure.RequestID, failure.Retryable
	}
	return "HEALTH_PROBE_FAILED", "", true
}

func cloneHealthObservation(value HealthObservation) HealthObservation {
	value.Compatibility.Reasons = append([]string(nil), value.Compatibility.Reasons...)
	value.Compatibility.Diagnostics = append([]string(nil), value.Compatibility.Diagnostics...)
	value.Compatibility.Retrieval.Reasons = append([]string(nil), value.Compatibility.Retrieval.Reasons...)
	value.Compatibility.Operations = append([]OperationCompatibility(nil), value.Compatibility.Operations...)
	for index := range value.Compatibility.Operations {
		value.Compatibility.Operations[index].Reasons = append([]string(nil), value.Compatibility.Operations[index].Reasons...)
	}
	return value
}
