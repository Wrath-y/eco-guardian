package sync

import (
	"strings"
	"testing"
)

func TestPipelineStatesAreClosed(t *testing.T) {
	if !StateReady.Valid() || PipelineState("other").Valid() {
		t.Fatal("pipeline state validation drift")
	}
}

func TestPipelineTransitionsAreClosed(t *testing.T) {
	if !StateQueued.CanTransitionTo(StateBuilding) || StateQueued.CanTransitionTo(StateReady) || StateReady.CanTransitionTo(StateBuilding) {
		t.Fatal("pipeline transition drift")
	}
}

func TestSyncStateBoundsSafeProviderDiagnostics(t *testing.T) {
	state := SyncState{RevisionID: "revision", Pipeline: StateQueued, Warnings: []string{}}
	if !state.Valid() {
		t.Fatal("valid state rejected")
	}
	state.ProviderRequestID = strings.Repeat("x", maxProviderIdentitySize+1)
	if state.Valid() {
		t.Fatal("oversized request identity accepted")
	}
	state.ProviderRequestID = "request"
	state.ProviderTaskID = "\xff"
	if state.Valid() {
		t.Fatal("invalid UTF-8 task identity accepted")
	}
	state.ProviderTaskID = "task"
	state.SafeError = strings.Repeat("x", maxSafeDiagnosticLength+1)
	if state.Valid() {
		t.Fatal("oversized safe diagnostic accepted")
	}
}
