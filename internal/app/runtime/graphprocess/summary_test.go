package graphprocess

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	platformprocess "github.com/zouyi/eco-guardian/internal/platform/process"
)

func TestFileSummaryStorePersistsOnlySafeNonAuthoritativeObservation(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "runtime", "process-summary.json")
	store := NewFileSummaryStore(filename)
	observation := ProcessObservation{
		Sequence: 1, State: StateReady, Ownership: platformprocess.OwnershipBundled, Generation: 8,
		DiagnosticPID: 1234, Endpoint: "http://127.0.0.1:9400", Attempt: 1,
		ObservedAt: time.Date(2026, 8, 25, 1, 2, 3, 0, time.UTC),
	}
	if err := store.SaveProcessSummary(observation); err != nil {
		t.Fatal(err)
	}
	observation.Sequence = 2
	observation.State = StateExited
	observation.ExitCode = 23
	if err := store.SaveProcessSummary(observation); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadProcessSummary()
	if err != nil || loaded != observation {
		t.Fatalf("loaded=%#v err=%v", loaded, err)
	}
	body, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"executable", "working_directory", "handle", "arguments", "environment"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("summary contains authority field %q: %s", forbidden, body)
		}
	}
}

func TestPersistedSummaryAndMismatchedGenerationCannotTerminate(t *testing.T) {
	process := newSupervisorProcess()
	process.generation = 9
	supervisor, _, _, _ := supervisorFixture(t, process)
	supervisor.mu.Lock()
	supervisor.current = process
	supervisor.mu.Unlock()
	if err := supervisor.TerminateOwned(8, 1); !errors.Is(err, platformprocess.ErrOwnership) || process.terminated {
		t.Fatalf("mismatched generation err=%v process=%#v", err, process)
	}
	if err := supervisor.TerminateOwned(9, 17); err != nil || !process.terminated {
		t.Fatalf("current generation err=%v process=%#v", err, process)
	}
	supervisor.mu.Lock()
	supervisor.current = nil
	supervisor.observation = ProcessObservation{Sequence: 5, State: StateReady, Ownership: platformprocess.OwnershipBundled, Generation: 9, DiagnosticPID: 42, ObservedAt: time.Now()}
	supervisor.mu.Unlock()
	if err := supervisor.TerminateOwned(9, 1); !errors.Is(err, platformprocess.ErrOwnership) {
		t.Fatalf("summary established authority: %v", err)
	}
}

func TestSummaryRejectsNonLoopbackEndpointAndUnsafeReason(t *testing.T) {
	store := NewFileSummaryStore(filepath.Join(t.TempDir(), "summary.json"))
	base := ProcessObservation{Sequence: 1, State: StateExternal, Ownership: platformprocess.OwnershipExternal, ObservedAt: time.Now()}
	base.Endpoint = "http://example.com:9400"
	if err := store.SaveProcessSummary(base); !errors.Is(err, ErrSupervisorInvalid) {
		t.Fatalf("non-loopback err=%v", err)
	}
	base.Endpoint = "http://127.0.0.1:9400"
	base.Reason = "raw\noutput"
	if err := store.SaveProcessSummary(base); !errors.Is(err, ErrSupervisorInvalid) {
		t.Fatalf("unsafe reason err=%v", err)
	}
}
