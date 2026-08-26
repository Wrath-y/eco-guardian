package recovery

import (
	"context"
	"errors"
	"reflect"
	"testing"

	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

func TestShutdownCoordinatorUsesGlobalSafePhases(t *testing.T) {
	calls := []string{}
	policy := func(kind string) ShutdownPolicy {
		operation := func(phase string) ShutdownOperation {
			return func(context.Context) error { calls = append(calls, phase+":"+kind); return nil }
		}
		return ShutdownPolicy{Kind: sharedjob.Kind(kind), StopClaims: operation("stop"), PersistIntent: operation("persist"), WaitSafeBoundary: operation("wait"), ProveRecoverable: operation("prove")}
	}
	coordinator, err := NewShutdownCoordinator(policy("graph"), policy("ai"))
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"stop:graph", "stop:ai", "persist:graph", "persist:ai", "wait:graph", "wait:ai", "prove:graph", "prove:ai"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
}

func TestShutdownCoordinatorHonorsDeadlineBeforeLaterBoundaries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	policy := ShutdownPolicy{Kind: "graph", StopClaims: func(context.Context) error { calls++; cancel(); return nil }, PersistIntent: func(context.Context) error { calls++; return nil }, WaitSafeBoundary: func(context.Context) error { calls++; return nil }, ProveRecoverable: func(context.Context) error { calls++; return nil }}
	coordinator, err := NewShutdownCoordinator(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = coordinator.Prepare(ctx); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestShutdownBoundaryCrashBeforeAndAfterCommitIsRetryableWithoutDuplicateEffect(t *testing.T) {
	for _, phase := range []string{"stop", "persist", "wait", "prove"} {
		for _, position := range []string{"before", "after"} {
			t.Run(phase+"-"+position, func(t *testing.T) {
				committed := map[string]bool{}
				effects := map[string]int{}
				failed := false
				operation := func(name string) ShutdownOperation {
					return func(context.Context) error {
						if committed[name] {
							return nil
						}
						if name == phase && !failed {
							failed = true
							if position == "before" {
								return errors.New("injected before boundary")
							}
							committed[name] = true
							effects[name]++
							return errors.New("injected after boundary")
						}
						committed[name] = true
						effects[name]++
						return nil
					}
				}
				policy := ShutdownPolicy{Kind: "job", StopClaims: operation("stop"), PersistIntent: operation("persist"), WaitSafeBoundary: operation("wait"), ProveRecoverable: operation("prove")}
				coordinator, err := NewShutdownCoordinator(policy)
				if err != nil {
					t.Fatal(err)
				}
				if err = coordinator.Prepare(context.Background()); err == nil {
					t.Fatal("expected injected crash")
				}
				if err = coordinator.Prepare(context.Background()); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"stop", "persist", "wait", "prove"} {
					if effects[name] != 1 {
						t.Fatalf("phase=%s effects=%v committed=%v", name, effects, committed)
					}
				}
			})
		}
	}
}
