package sync

import "strings"

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

func (s SyncState) Valid() bool {
	if s.RevisionID == "" || !s.Pipeline.Valid() || s.Generation < 0 || len(s.SafeError) > 1024 || len(s.Warnings) > 32 {
		return false
	}
	for _, warning := range s.Warnings {
		if strings.TrimSpace(warning) == "" || len(warning) > 256 {
			return false
		}
	}
	return true
}
