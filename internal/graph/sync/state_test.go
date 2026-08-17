package sync

import "testing"

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
