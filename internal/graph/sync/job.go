package sync

import (
	"context"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
)

// JobStatus mirrors the existing durable #7 Job lifecycle. Graph adds
// checkpoint detail through events rather than creating a second job table.
type JobStatus string

const (
	JobQueued      JobStatus = "queued"
	JobRunning     JobStatus = "running"
	JobSucceeded   JobStatus = "succeeded"
	JobFailed      JobStatus = "failed"
	JobCanceled    JobStatus = "canceled"
	JobInterrupted JobStatus = "interrupted"
)

func (s JobStatus) Valid() bool {
	switch s {
	case JobQueued, JobRunning, JobSucceeded, JobFailed, JobCanceled, JobInterrupted:
		return true
	default:
		return false
	}
}

func (s JobStatus) CanTransitionTo(next JobStatus) bool {
	switch s {
	case JobQueued:
		return next == JobRunning || next == JobFailed || next == JobCanceled || next == JobInterrupted
	case JobRunning:
		return next == JobSucceeded || next == JobFailed || next == JobCanceled || next == JobInterrupted
	case JobInterrupted:
		return next == JobSucceeded || next == JobFailed
	default:
		return false
	}
}

func (s JobStatus) Terminal() bool {
	return s == JobSucceeded || s == JobFailed || s == JobCanceled
}

type GraphJobResult struct {
	Type string
	ID   domain.ID
	URL  string
}

func (r GraphJobResult) Valid() bool {
	return strings.TrimSpace(r.Type) != "" && r.ID.Valid() && strings.TrimSpace(r.URL) != ""
}

// GraphJobEvent is immutable audit evidence for an observed worker checkpoint.
// Ordinals and progress are monotonic per job; exact replays are deduplicated.
type GraphJobEvent struct {
	JobID     domain.ID
	Ordinal   int64
	Phase     WorkerPhase
	Progress  int
	Warning   string
	SafeError string
	Result    *GraphJobResult
}

func (e GraphJobEvent) Valid() bool {
	return e.JobID.Valid() && e.Ordinal > 0 && e.Phase.Valid() && e.Progress >= 0 && e.Progress <= 100 && len(e.Warning) <= 256 && len(e.SafeError) <= 1024 && (e.Result == nil || e.Result.Valid())
}

// DurableJobStore and JobEventStore expose the existing #7 job/event storage
// at a Graph-specific, transport-neutral boundary.
type DurableJobStore interface {
	GetGraphJob(context.Context, domain.ID) (GraphJob, error)
	TransitionGraphJob(context.Context, domain.ID, JobStatus, JobStatus, *GraphJobResult) (GraphJob, bool, error)
}

type JobEventStore interface {
	AppendGraphJobEvent(context.Context, GraphJobEvent) (GraphJobEvent, bool, error)
	ListGraphJobEvents(context.Context, domain.ID, int64) ([]GraphJobEvent, error)
}
