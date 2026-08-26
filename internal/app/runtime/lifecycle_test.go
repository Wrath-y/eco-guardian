package runtime

import (
	"sync"
	"testing"
	"time"
)

func TestStatusStorePublishesMonotonicDetachedSnapshots(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	store := NewStatusStore(func() time.Time { now = now.Add(time.Second); return now })
	id, updates := store.Subscribe()
	defer store.Unsubscribe(id)
	initial := <-updates
	if initial.Generation != 0 || initial.Phase != PhaseLoadingSettings {
		t.Fatalf("initial=%#v", initial)
	}
	first, err := store.Transition(PhaseVerifyingPackage, func(snapshot *StatusSnapshot) {
		snapshot.Reasons = []RuntimeReason{{Code: "PACKAGE_PENDING", Component: "package", Message: "Package verification is pending"}}
		snapshot.Observations = []RuntimeObservation{{ID: "package", State: "checking", Generation: 1, ObservedAt: now}}
	})
	if err != nil || first.Generation != 1 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	first.Reasons[0].Code = "MUTATED"
	first.Observations[0].State = "mutated"
	stored := store.Snapshot()
	if stored.Reasons[0].Code != "PACKAGE_PENDING" || stored.Observations[0].State != "checking" {
		t.Fatalf("snapshot mutation escaped: %#v", stored)
	}
	observed := <-updates
	if observed.Generation != 1 || observed.UpdatedAt.IsZero() {
		t.Fatalf("observed=%#v", observed)
	}
}

func TestStatusStoreSupportsConcurrentReadersDuringPublication(t *testing.T) {
	store := NewStatusStore(nil)
	var wait sync.WaitGroup
	errorsFound := make(chan string, 32)
	for reader := 0; reader < 32; reader++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			var previous uint64
			for index := 0; index < 2_000; index++ {
				snapshot := store.Snapshot()
				if snapshot.Generation < previous || !snapshot.Phase.Valid() || snapshot.SchemaVersion != 1 {
					errorsFound <- "reader observed torn or reverse snapshot"
					return
				}
				previous = snapshot.Generation
			}
		}()
	}
	for _, phase := range []Phase{PhaseVerifyingPackage, PhaseBindingHTTP, PhaseStartingDependencies, PhaseRecovering, PhaseOpeningRecentProject, PhaseReady} {
		if _, err := store.Transition(phase, func(snapshot *StatusSnapshot) {
			snapshot.Observations = []RuntimeObservation{{ID: string(phase), State: "observed", Generation: snapshot.Generation}}
		}); err != nil {
			t.Fatal(err)
		}
	}
	wait.Wait()
	close(errorsFound)
	for message := range errorsFound {
		t.Fatal(message)
	}
	if final := store.Snapshot(); final.Generation != 6 || final.Phase != PhaseReady {
		t.Fatalf("final=%#v", final)
	}
}

func TestStatusStoreDropsStaleObserverValuesWithoutBlocking(t *testing.T) {
	store := NewStatusStore(nil)
	_, updates := store.Subscribe()
	if _, err := store.Transition(PhaseVerifyingPackage, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(PhaseBindingHTTP, nil); err != nil {
		t.Fatal(err)
	}
	latest := <-updates
	if latest.Generation != 2 || latest.Phase != PhaseBindingHTTP {
		t.Fatalf("latest=%#v", latest)
	}
}

func TestStatusStoreRejectsSkippedOrReverseLifecycle(t *testing.T) {
	store := NewStatusStore(nil)
	if _, err := store.Transition(PhaseBindingHTTP, nil); err != ErrRuntimeTransitionInvalid {
		t.Fatalf("skipped transition err=%v", err)
	}
	if _, err := store.Transition(PhaseVerifyingPackage, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(PhaseLoadingSettings, nil); err != ErrRuntimeTransitionInvalid {
		t.Fatalf("reverse transition err=%v", err)
	}
	if _, err := store.Transition(PhaseStopping, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(PhaseStopped, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(PhaseReady, nil); err != ErrRuntimeTransitionInvalid {
		t.Fatalf("post-stop transition err=%v", err)
	}
}
