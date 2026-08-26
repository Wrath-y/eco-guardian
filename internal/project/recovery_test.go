package project

import (
	"context"
	"reflect"
	"testing"

	runtimerecovery "github.com/zouyi/eco-guardian/internal/app/runtime/recovery"
	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

func TestProjectRecoveryStagesUseSafetyOrderBeforeWorkers(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	projectStore, _, err := store.Create(context.Background(), t.TempDir(), registry)
	if err != nil {
		t.Fatal(err)
	}
	defer projectStore.Close()
	calls := []string{}
	stage := func(name string) RecoveryStage {
		return RecoveryStage{
			Scan: func(context.Context, *store.Store) (runtimerecovery.ScanResult, error) {
				calls = append(calls, "scan:"+name)
				return runtimerecovery.ScanResult{}, nil
			},
			Worker: func(context.Context, *store.Store) error { calls = append(calls, "worker:"+name); return nil },
		}
	}
	stages := RecoveryStages{Remaining: stage("remaining"), Graph: stage("graph"), Release: stage("release"), Deterministic: stage("deterministic"), RestoreCompatibility: stage("restore")}
	if err := stages.Recover(context.Background(), projectStore); err != nil {
		t.Fatal(err)
	}
	want := []string{"scan:restore", "worker:restore", "scan:release", "worker:release", "scan:graph", "worker:graph", "scan:deterministic", "worker:deterministic", "scan:remaining", "worker:remaining"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
}
