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
type CancellationCheck func(context.Context) error

func ExecuteSamples(ctx context.Context, executor contract.BoundedExecutor, plan SamplePlan, run SampleRunner) ([]SampleResult, error) {
	return ExecuteSamplesWithCancellation(ctx, executor, plan, nil, run)
}

// ExecuteSamplesWithCancellation observes persisted cancellation before work
// is scheduled and again before a completed sample is exposed for checkpointing.
func ExecuteSamplesWithCancellation(ctx context.Context, executor contract.BoundedExecutor, plan SamplePlan, check CancellationCheck, run SampleRunner) ([]SampleResult, error) {
	if executor == nil || run == nil || plan.SampleCount < 1 || plan.Workers < 1 {
		return nil, ErrSamplePlanInvalid
	}
	if err := checkCancellation(ctx, check); err != nil {
		return nil, err
	}
	results := make([]SampleResult, plan.SampleCount)
	var (
		mu       sync.Mutex
		firstErr error
	)
	err := executor.Run(ctx, plan.Workers, func(workerCtx context.Context, worker int) error {
		for _, ordinal := range plan.OrdinalsForWorker(worker) {
			if checkErr := checkCancellation(workerCtx, check); checkErr != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = checkErr
				}
				mu.Unlock()
				return nil
			}
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
			if checkErr := checkCancellation(workerCtx, check); checkErr != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = checkErr
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

func checkCancellation(ctx context.Context, check CancellationCheck) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if check != nil {
		return check(ctx)
	}
	return nil
}
