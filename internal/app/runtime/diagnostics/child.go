package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/zouyi/eco-guardian/internal/app/runtime/graphprocess"
)

// ChildSink converts untrusted child output into bounded metadata. The raw
// line is intentionally not retained because it may contain arbitrary Graph
// or project business content even after credential pattern filtering.
type ChildSink struct{ Logger *Logger }

func (sink ChildSink) PublishChildEvent(value graphprocess.ChildOutputEvent) {
	if sink.Logger == nil {
		return
	}
	digest := sha256.Sum256([]byte(value.Line))
	sink.Logger.Emit(Event{
		Name: EventChildOutput, Component: "local-rag", State: "observed",
		Correlation: Correlation{LaunchGeneration: uint64(value.Generation)},
		Fields: map[string]any{
			"stream": string(value.Stream), "line_bytes": len(value.Line), "line_sha256": hex.EncodeToString(digest[:]),
			"truncated": value.Truncated, "dropped_before": value.DroppedBefore,
		},
	})
}

var _ graphprocess.ChildEventSink = ChildSink{}
