package orchestration

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/zouyi/eco-guardian/internal/simulation/contract"
)

var ErrSamplePlanInvalid = errors.New("simulation sample plan is invalid")

// SampleResult is copied at every execution boundary. It holds only one
// completed ordinal; aggregate state must never be updated by a worker.
type SampleResult struct {
	Ordinal         uint64
	InputHash       string
	FingerprintHash string
	Payload         []byte
}

func (result SampleResult) Clone() SampleResult {
	result.Payload = append([]byte(nil), result.Payload...)
	return result
}

type SamplePlan struct{ SampleCount, Workers int }

func NewSamplePlan(sampleCount, workers int) (SamplePlan, error) {
	if sampleCount < 1 || workers < 1 {
		return SamplePlan{}, ErrSamplePlanInvalid
	}
	if workers > sampleCount {
		workers = sampleCount
	}
	return SamplePlan{SampleCount: sampleCount, Workers: workers}, nil
}

// OrdinalsForWorker uses a fixed strided partition. Scheduling order is not a
// semantic input; all consumers receive output reassembled by ordinal.
func (plan SamplePlan) OrdinalsForWorker(worker int) []uint64 {
	if worker < 0 || worker >= plan.Workers {
		return nil
	}
	ordinals := make([]uint64, 0, (plan.SampleCount+plan.Workers-1-worker)/plan.Workers)
	for ordinal := worker; ordinal < plan.SampleCount; ordinal += plan.Workers {
		ordinals = append(ordinals, uint64(ordinal))
	}
	return ordinals
}

type SampleRunner func(context.Context, uint64) (SampleResult, error)

func ExecuteSamples(ctx context.Context, executor contract.BoundedExecutor, plan SamplePlan, run SampleRunner) ([]SampleResult, error) {
	if executor == nil || run == nil || plan.SampleCount < 1 || plan.Workers < 1 {
		return nil, ErrSamplePlanInvalid
	}
	results := make([]SampleResult, plan.SampleCount)
	var (
		mu       sync.Mutex
		firstErr error
	)
	err := executor.Run(ctx, plan.Workers, func(workerCtx context.Context, worker int) error {
		for _, ordinal := range plan.OrdinalsForWorker(worker) {
			mu.Lock()
			failed := firstErr != nil
			mu.Unlock()
			if failed {
				return nil
			}
			result, runErr := run(workerCtx, ordinal)
			if runErr == nil && result.Ordinal != ordinal {
				runErr = fmt.Errorf("sample runner returned ordinal %d for %d", result.Ordinal, ordinal)
			}
			if runErr != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = runErr
				}
				mu.Unlock()
				return nil
			}
			results[ordinal] = result.Clone()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	mu.Lock()
	defer mu.Unlock()
	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}
