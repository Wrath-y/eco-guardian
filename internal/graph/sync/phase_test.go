package sync

import "testing"

func TestWorkerPhaseAdvancesOneCheckpointAtATime(t *testing.T) {
	if !PhaseQueued.CanAdvanceTo(PhaseValidationConfirmed) || !PhasePolling.CanAdvanceTo(PhaseVerifying) || !PhaseReady.CanAdvanceTo(PhaseReady) {
		t.Fatal("valid phase transition rejected")
	}
	if PhaseQueued.CanAdvanceTo(PhaseProjected) || PhaseReady.CanAdvanceTo(PhasePolling) || WorkerPhase("unknown").CanAdvanceTo(PhaseQueued) {
		t.Fatal("invalid phase transition accepted")
	}
}
