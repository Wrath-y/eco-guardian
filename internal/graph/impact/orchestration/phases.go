package orchestration

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type Phase string

const (
	PhaseAdmission         Phase = "ADMISSION"
	PhaseDiff              Phase = "DIFF"
	PhaseTraversal         Phase = "TRAVERSAL"
	PhaseDefaultPaths      Phase = "DEFAULT_PATHS"
	PhaseOptionalRetrieval Phase = "OPTIONAL_RETRIEVAL"
	PhaseVerification      Phase = "VERIFICATION"
	PhaseFinalCommit       Phase = "FINAL_COMMIT"
)

func appendPhase(ctx context.Context, events impact.EventStore, clock impact.Clock, jobID domain.ID, phase Phase, progress int, warning, safeError string, result *sharedjob.Result) error {
	if events == nil || clock == nil {
		return nil
	}
	existing, err := events.ListEvents(ctx, jobID, 0)
	if err != nil {
		return err
	}
	for _, event := range existing {
		if event.Phase == string(phase) {
			return nil
		}
	}
	ordinal := int64(len(existing) + 1)
	_, _, err = events.Append(ctx, sharedjob.Event{JobID: jobID, Ordinal: ordinal, Phase: string(phase), Progress: progress, Warning: warning, SafeError: safeError, Result: result, CreatedAt: clock.Now().UTC()})
	return err
}

func terminalFailure(ctx context.Context, jobs impact.JobStore, events impact.EventStore, clock impact.Clock, job sharedjob.Record, err error) error {
	if err == nil {
		return nil
	}
	next := sharedjob.Failed
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		next = sharedjob.Interrupted
	}
	current, getErr := jobs.GetJob(ctx, job.ID)
	if getErr != nil {
		return err
	}
	if current.Status == sharedjob.Succeeded || current.Status == sharedjob.Failed || current.Status == sharedjob.Canceled {
		return err
	}
	_, _, _ = jobs.Transition(ctx, current.ID, current.Status, next, nil, current.CancelGeneration)
	_ = appendPhase(ctx, events, clockOrFixed(clock), job.ID, PhaseFinalCommit, 100, "", safeError(err), nil)
	return err
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	for _, code := range impact.ErrorCodes {
		if err.Error() == code || strings.Contains(err.Error(), code+":") {
			return code
		}
	}
	return "INTERNAL_ERROR"
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Now().UTC() }
func clockOrFixed(clock impact.Clock) impact.Clock {
	if clock == nil {
		return fixedClock{}
	}
	return clock
}
