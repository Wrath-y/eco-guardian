package orchestration

import (
	"context"
	"sync"
	"testing"

	"github.com/zouyi/eco-guardian/internal/simulation/contract"
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
