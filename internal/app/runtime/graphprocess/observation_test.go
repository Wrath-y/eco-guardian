package graphprocess

import (
	"context"
	"sync"
	"testing"
	"time"
)

type healthSourceFake struct {
	mu     sync.Mutex
	calls  int
	block  <-chan struct{}
	result HealthCompatibility
	err    error
}

func (source *healthSourceFake) ObserveHealth(ctx context.Context, _ string) (HealthCompatibility, error) {
	source.mu.Lock()
	source.calls++
	source.mu.Unlock()
	if source.block != nil {
		select {
		case <-source.block:
		case <-ctx.Done():
			return HealthCompatibility{}, ctx.Err()
		}
	}
	return source.result, source.err
}

func (source *healthSourceFake) callCount() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.calls
}

func healthyCompatibility() HealthCompatibility {
	return HealthCompatibility{Compatible: true, Status: "ok", Reasons: []string{}, Diagnostics: []string{}, Operations: []OperationCompatibility{{ID: OperationCoreQuery, State: OperationAvailable}}}
}

func TestHealthObserverCachesWithinTTLAndInvalidatesWithoutSideEffects(t *testing.T) {
	clock := &supervisorClock{now: time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC)}
	source := &healthSourceFake{result: healthyCompatibility()}
	observer, err := NewHealthObserver(HealthObserverOptions{Source: source, Clock: clock, Timeout: time.Second, TTL: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	first, err := observer.Observe(t.Context(), "http://127.0.0.1:9400")
	if err != nil {
		t.Fatal(err)
	}
	second, _ := observer.Observe(t.Context(), "http://127.0.0.1:9400")
	if source.callCount() != 1 || first.Generation != 1 || second.Generation != 1 || first.State != HealthHealthy {
		t.Fatalf("first=%#v second=%#v calls=%d", first, second, source.callCount())
	}
	observer.Invalidate()
	if source.callCount() != 1 {
		t.Fatal("invalidation performed a probe")
	}
	third, _ := observer.Observe(t.Context(), "http://127.0.0.1:9400")
	if source.callCount() != 2 || third.Generation != 2 {
		t.Fatalf("third=%#v calls=%d", third, source.callCount())
	}
	clock.advance(6 * time.Second)
	fourth, _ := observer.Observe(t.Context(), "http://127.0.0.1:9400")
	if source.callCount() != 3 || fourth.Generation != 3 {
		t.Fatalf("fourth=%#v calls=%d", fourth, source.callCount())
	}
}

func TestHealthObserverSingleFlightsConcurrentRefresh(t *testing.T) {
	release := make(chan struct{})
	source := &healthSourceFake{block: release, result: healthyCompatibility()}
	observer, err := NewHealthObserver(HealthObserverOptions{Source: source, Timeout: time.Second, TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan HealthObservation, 20)
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, _ := observer.Observe(t.Context(), "http://127.0.0.1:9400")
			results <- value
		}()
	}
	deadline := time.Now().Add(time.Second)
	for source.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wait.Wait()
	close(results)
	if source.callCount() != 1 {
		t.Fatalf("calls=%d", source.callCount())
	}
	for result := range results {
		if result.Generation != 1 {
			t.Fatalf("result=%#v", result)
		}
	}
}

func TestHealthObserverInvalidationDuringProbeForcesNextRefresh(t *testing.T) {
	release := make(chan struct{})
	source := &healthSourceFake{block: release, result: healthyCompatibility()}
	observer, err := NewHealthObserver(HealthObserverOptions{Source: source, Timeout: time.Second, TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan HealthObservation, 1)
	go func() { value, _ := observer.Observe(t.Context(), "http://127.0.0.1:9400"); done <- value }()
	deadline := time.Now().Add(time.Second)
	for source.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	observer.Invalidate()
	close(release)
	first := <-done
	if !first.ExpiresAt.IsZero() {
		t.Fatalf("invalidated in-flight observation remained fresh: %#v", first)
	}
	if _, err = observer.Observe(t.Context(), "http://127.0.0.1:9400"); err != nil {
		t.Fatal(err)
	}
	if source.callCount() != 2 {
		t.Fatalf("calls=%d", source.callCount())
	}
}

func TestHealthObserverBoundsTimeoutAndPublishesSafeFailure(t *testing.T) {
	source := &healthSourceFake{block: make(chan struct{})}
	observer, err := NewHealthObserver(HealthObserverOptions{Source: source, Timeout: 20 * time.Millisecond, TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	observation, err := observer.Observe(t.Context(), "http://127.0.0.1:9400")
	if err != nil || time.Since(started) > time.Second || observation.State != HealthUnavailable || observation.Reason != "HEALTH_TIMEOUT" || !observation.Retryable {
		t.Fatalf("observation=%#v err=%v elapsed=%s", observation, err, time.Since(started))
	}
	source = &healthSourceFake{err: HealthProbeFailure{Code: "HEALTH_CONTRACT_INVALID", RequestID: "safe-request", Retryable: false}}
	observer, _ = NewHealthObserver(HealthObserverOptions{Source: source, Timeout: time.Second, TTL: time.Second})
	observation, _ = observer.Observe(t.Context(), "http://127.0.0.1:9400")
	if observation.Reason != "HEALTH_CONTRACT_INVALID" || observation.RequestID != "safe-request" || observation.Retryable {
		t.Fatalf("observation=%#v", observation)
	}
}

type sideEffectHealthSource struct {
	healthCalls, processStarts, snapshotSubmissions, rebuilds, jobs, modelCalls, exactInspections int
}

func (source *sideEffectHealthSource) ObserveHealth(context.Context, string) (HealthCompatibility, error) {
	source.healthCalls++
	return healthyCompatibility(), nil
}

func (source *sideEffectHealthSource) StartProcess()         { source.processStarts++ }
func (source *sideEffectHealthSource) SubmitSnapshot()       { source.snapshotSubmissions++ }
func (source *sideEffectHealthSource) RebuildSnapshot()      { source.rebuilds++ }
func (source *sideEffectHealthSource) CreateJob()            { source.jobs++ }
func (source *sideEffectHealthSource) InvokeModel()          { source.modelCalls++ }
func (source *sideEffectHealthSource) InspectExactSnapshot() { source.exactInspections++ }

func TestHealthObservationPollingIsReadOnlyAndNotAdmission(t *testing.T) {
	source := &sideEffectHealthSource{}
	observer, err := NewHealthObserver(HealthObserverOptions{Source: source, Timeout: time.Second, TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20; index++ {
		if _, err = observer.Observe(t.Context(), "http://127.0.0.1:9400"); err != nil {
			t.Fatal(err)
		}
		_ = observer.Snapshot()
	}
	observer.Invalidate()
	_ = observer.Snapshot()
	if source.healthCalls != 1 || source.processStarts != 0 || source.snapshotSubmissions != 0 || source.rebuilds != 0 || source.jobs != 0 || source.modelCalls != 0 || source.exactInspections != 0 {
		t.Fatalf("health observer crossed admission boundary: %#v", source)
	}
}
