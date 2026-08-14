package sync

type PipelineState string

const (
	StateSaved             PipelineState = "saved"
	StateValidating        PipelineState = "validating"
	StateBlockedValidation PipelineState = "blocked_validation"
	StateQueued            PipelineState = "graph_queued"
	StateBuilding          PipelineState = "graph_building"
	StateReady             PipelineState = "graph_ready"
	StateFailed            PipelineState = "graph_failed"
)

func (s PipelineState) Valid() bool {
	switch s {
	case StateSaved, StateValidating, StateBlockedValidation, StateQueued, StateBuilding, StateReady, StateFailed:
		return true
	default:
		return false
	}
}

type SyncState struct {
	RevisionID     string
	Pipeline       PipelineState
	LatestJobID    string
	ExternalTaskID string
	Generation     int64
	SafeError      string
	Warnings       []string
}

func (s SyncState) Valid() bool { return s.RevisionID != "" && s.Pipeline.Valid() && s.Generation >= 0 }
