package project

import (
	"context"

	runtimerecovery "github.com/zouyi/eco-guardian/internal/app/runtime/recovery"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type ProjectRecoveryStage func(context.Context, *store.Store) (runtimerecovery.ScanResult, error)
type ProjectWorkerStarter func(context.Context, *store.Store) error

type RecoveryStage struct {
	Scan   ProjectRecoveryStage
	Worker ProjectWorkerStarter
}

// RecoveryStages is the project-open adapter for the process recovery order.
// SQLite journal/schema/migration checks have committed before this adapter is
// called; subsequent module scans retain their central fixed order.
type RecoveryStages struct {
	RestoreCompatibility RecoveryStage
	Release              RecoveryStage
	Graph                RecoveryStage
	Deterministic        RecoveryStage
	Remaining            RecoveryStage
}

func (stages RecoveryStages) Recover(ctx context.Context, project *store.Store) error {
	descriptors := make([]runtimerecovery.StageDescriptor, 0, 5)
	appendStage := func(stage runtimerecovery.Stage, value RecoveryStage) {
		if value.Scan == nil && value.Worker == nil {
			return
		}
		descriptor := runtimerecovery.StageDescriptor{Stage: stage}
		if value.Scan != nil {
			descriptor.Scanner = runtimerecovery.ScannerFunc(func(ctx context.Context) (runtimerecovery.ScanResult, error) { return value.Scan(ctx, project) })
		}
		if value.Worker != nil {
			descriptor.Worker = runtimerecovery.WorkerStarterFunc(func(ctx context.Context) error { return value.Worker(ctx, project) })
		}
		descriptors = append(descriptors, descriptor)
	}
	appendStage(runtimerecovery.StageRestoreCompatibility, stages.RestoreCompatibility)
	appendStage(runtimerecovery.StageRelease, stages.Release)
	appendStage(runtimerecovery.StageGraph, stages.Graph)
	appendStage(runtimerecovery.StageDeterministic, stages.Deterministic)
	appendStage(runtimerecovery.StageRemaining, stages.Remaining)
	coordinator, err := runtimerecovery.NewCoordinator(descriptors...)
	if err != nil {
		return err
	}
	_, err = coordinator.Recover(ctx)
	return err
}
