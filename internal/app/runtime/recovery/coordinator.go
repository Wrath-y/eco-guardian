package recovery

import (
	"context"
	"sort"
)

type Stage string

const (
	StageRestoreCompatibility Stage = "restore_compatibility_migration"
	StageRelease              Stage = "release_intent_pointer"
	StageGraph                Stage = "graph_external_task"
	StageDeterministic        Stage = "deterministic_impact_simulation_risk"
	StageRemaining            Stage = "remaining_queued_work"
)

var stageOrder = map[Stage]int{
	StageRestoreCompatibility: 0,
	StageRelease:              1,
	StageGraph:                2,
	StageDeterministic:        3,
	StageRemaining:            4,
}

type ScanResult struct {
	Scanned, Recovered, RecoveryRequired int
}

type Scanner interface {
	ScanAndRecover(context.Context) (ScanResult, error)
}

type ScannerFunc func(context.Context) (ScanResult, error)

func (function ScannerFunc) ScanAndRecover(ctx context.Context) (ScanResult, error) {
	return function(ctx)
}

type WorkerStarter interface{ Start(context.Context) error }

type WorkerStarterFunc func(context.Context) error

func (function WorkerStarterFunc) Start(ctx context.Context) error { return function(ctx) }

type StageDescriptor struct {
	Stage   Stage
	Scanner Scanner
	Worker  WorkerStarter
}

type StageResult struct {
	Stage Stage
	ScanResult
	ScanCommitted bool
	WorkerStarted bool
}

// Coordinator runs registered recovery stages in one fixed safety order.
// A stage's normal worker starts only after that stage's scan has returned and
// all earlier scan/worker boundaries have committed successfully.
type Coordinator struct{ stages []StageDescriptor }

func NewCoordinator(descriptors ...StageDescriptor) (*Coordinator, error) {
	seen := map[Stage]bool{}
	stages := append([]StageDescriptor(nil), descriptors...)
	for _, descriptor := range stages {
		if _, valid := stageOrder[descriptor.Stage]; !valid || seen[descriptor.Stage] || (descriptor.Scanner == nil && descriptor.Worker == nil) {
			return nil, ErrRegistryInvalid
		}
		seen[descriptor.Stage] = true
	}
	sort.Slice(stages, func(left, right int) bool { return stageOrder[stages[left].Stage] < stageOrder[stages[right].Stage] })
	return &Coordinator{stages: stages}, nil
}

func (coordinator *Coordinator) Recover(ctx context.Context) ([]StageResult, error) {
	if coordinator == nil {
		return nil, ErrRegistryInvalid
	}
	results := make([]StageResult, 0, len(coordinator.stages))
	for _, stage := range coordinator.stages {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		result := StageResult{Stage: stage.Stage}
		if stage.Scanner != nil {
			observed, err := stage.Scanner.ScanAndRecover(ctx)
			if err != nil {
				return append(results, result), err
			}
			if observed.Scanned < 0 || observed.Recovered < 0 || observed.RecoveryRequired < 0 || observed.Recovered+observed.RecoveryRequired > observed.Scanned {
				return append(results, result), ErrFactsInvalid
			}
			result.ScanResult = observed
		}
		result.ScanCommitted = true
		if stage.Worker != nil {
			if err := stage.Worker.Start(ctx); err != nil {
				return append(results, result), err
			}
			result.WorkerStarted = true
		}
		results = append(results, result)
	}
	return results, nil
}

func IsRecoveryRequired(results []StageResult) bool {
	for _, result := range results {
		if result.RecoveryRequired > 0 {
			return true
		}
	}
	return false
}
