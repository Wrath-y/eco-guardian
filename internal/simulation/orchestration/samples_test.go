package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/metric"
)

type concurrentExecutor struct{}

func (concurrentExecutor) Run(ctx context.Context, workers int, run func(context.Context, int) error) error {
	var group sync.WaitGroup
	errors := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func(index int) { defer group.Done(); errors <- run(ctx, index) }(worker)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}

var _ contract.BoundedExecutor = concurrentExecutor{}

func TestSamplePlanCoversStableOrdinalsAndReassemblesConcurrentResults(t *testing.T) {
	plan, err := NewSamplePlan(10, 3)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uint64]bool{}
	for worker := 0; worker < plan.Workers; worker++ {
		for _, ordinal := range plan.OrdinalsForWorker(worker) {
			if seen[ordinal] {
				t.Fatalf("duplicate ordinal %d", ordinal)
			}
			seen[ordinal] = true
		}
	}
	results, err := ExecuteSamples(context.Background(), concurrentExecutor{}, plan, func(_ context.Context, ordinal uint64) (SampleResult, error) {
		return SampleResult{Ordinal: ordinal, InputHash: "input", FingerprintHash: "fingerprint", Payload: []byte{byte(ordinal)}}, nil
	})
	if err != nil || len(results) != 10 {
		t.Fatalf("results=%d err=%v", len(results), err)
	}
	for ordinal, result := range results {
		if result.Ordinal != uint64(ordinal) || result.Payload[0] != byte(ordinal) {
			t.Fatalf("ordinal=%d result=%#v", ordinal, result)
		}
	}
}

func TestSamplePlanBoundsWorkersAndCopiesWorkerPayload(t *testing.T) {
	plan, err := NewSamplePlan(2, 99)
	if err != nil || plan.Workers != 2 {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	payload := []byte("sample")
	results, err := ExecuteSamples(context.Background(), concurrentExecutor{}, plan, func(_ context.Context, ordinal uint64) (SampleResult, error) {
		return SampleResult{Ordinal: ordinal, Payload: payload}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = '!'
	if string(results[0].Payload) != "sample" {
		t.Fatalf("result payload leaked worker mutation: %q", results[0].Payload)
	}
}

func TestSampleResultsKeepHashesAndFinalMetricHashAcrossWorkerCounts(t *testing.T) {
	registry, err := metric.V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	inputHash, fingerprintHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	var expectedResultHash string
	expectedSampleHashes := map[uint64]string{}
	for _, workers := range []int{1, 2, 3, 12} {
		plan, err := NewSamplePlan(12, workers)
		if err != nil {
			t.Fatal(err)
		}
		results, err := ExecuteSamples(context.Background(), concurrentExecutor{}, plan, func(_ context.Context, ordinal uint64) (SampleResult, error) {
			time.Sleep(time.Duration((ordinal*7)%5) * time.Millisecond)
			return SampleResult{Ordinal: ordinal, InputHash: inputHash, FingerprintHash: fingerprintHash, Payload: []byte(fmt.Sprintf("sample-%d", ordinal))}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		samples := make([]metric.Sample, 0, len(results))
		for _, result := range results {
			value, parseErr := formula.ParseDecimal(fmt.Sprintf("%d", result.Ordinal%3+1))
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			observations := []metric.Observation{{ID: "damage_per_second", Unit: "points_per_second", Value: value}, {ID: "healing_per_second", Unit: "points_per_second", Value: value}, {ID: "survival_seconds", Unit: "milliseconds", Value: value}, {ID: "resource_efficiency", Unit: "ratio", Value: value}, {ID: "control_duration", Unit: "milliseconds", Value: value}}
			if result.Ordinal%2 == 1 {
				observations[0], observations[4] = observations[4], observations[0]
			}
			samples = append(samples, metric.Sample{Ordinal: result.Ordinal, Status: metric.SampleSucceeded, Observations: observations})
		}
		aggregates, reduceErr := metric.ReduceExpected(registry, samples, plan.SampleCount)
		if reduceErr != nil {
			t.Fatal(reduceErr)
		}
		canonical, resultErr := metric.NewCanonicalResult(inputHash, fingerprintHash, aggregates, nil)
		if resultErr != nil {
			t.Fatal(resultErr)
		}
		resultHash, hashErr := canonical.Hash()
		if hashErr != nil {
			t.Fatal(hashErr)
		}
		for _, sample := range samples {
			sampleHash, sampleErr := metric.SampleHash(sample)
			if sampleErr != nil {
				t.Fatal(sampleErr)
			}
			if expected, found := expectedSampleHashes[sample.Ordinal]; found && sampleHash != expected {
				t.Fatalf("workers=%d sample %d hash changed: %s != %s", workers, sample.Ordinal, sampleHash, expected)
			} else {
				expectedSampleHashes[sample.Ordinal] = sampleHash
			}
		}
		if expectedResultHash == "" {
			expectedResultHash = resultHash
		} else if resultHash != expectedResultHash {
			t.Fatalf("workers=%d result hash changed: %s != %s", workers, resultHash, expectedResultHash)
		}
	}
}

func TestSampleExecutionChecksCancellationBeforeSchedulingAndExposure(t *testing.T) {
	plan, err := NewSamplePlan(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("canceled")
	checks := 0
	_, err = ExecuteSamplesWithCancellation(context.Background(), concurrentExecutor{}, plan, func(context.Context) error {
		checks++
		if checks == 3 {
			return stop
		}
		return nil
	}, func(_ context.Context, ordinal uint64) (SampleResult, error) {
		return SampleResult{Ordinal: ordinal}, nil
	})
	if !errors.Is(err, stop) || checks != 3 {
		t.Fatalf("checks=%d err=%v", checks, err)
	}
}
