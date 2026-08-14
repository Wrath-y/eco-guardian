package sync

import "testing"

func TestPipelineStatesAreClosed(t *testing.T) {
	if !StateReady.Valid() || PipelineState("other").Valid() {
		t.Fatal("pipeline state validation drift")
	}
}
