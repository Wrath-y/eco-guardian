package release

import (
	"context"
	"errors"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrRecoveryRequired = errors.New("release intent requires recovery")

type RecoveryOutcome string

const (
	RecoveryCompleted       RecoveryOutcome = "completed"
	RecoveryContinued       RecoveryOutcome = "continued"
	RecoveryFailed          RecoveryOutcome = "failed"
	RecoveryRequiresSupport RecoveryOutcome = "recovery_required"
)

// RecoveryResult is a durable-saga reconciliation decision. A
// recovery_required result is also written as a failed intent with explicit
// diagnostic detail; it is never guessed to be a successful release.
type RecoveryResult struct {
	IntentID domain.ID
	Outcome  RecoveryOutcome
	Err      error
}

// IntentRecovery runs when a project is opened. It only trusts three durable
// facts: the intent/Job, SQLite's active pointer, and the Graph's explicit
// active snapshot. It replays the same idempotency key for unfinished Graph
// activation, commits through the original atomic commit port, or restores
// the recorded previous Graph identity before recording an explainable failure.
type IntentRecovery struct {
	Intents IntentRecoveryRepository
	Jobs    DurableJobRepository
	Sources CommandSources
	Commit  ActivatedIntentCommitter
	Graph   GraphRecoveryGate
}

func (r IntentRecovery) Recover(ctx context.Context) ([]RecoveryResult, error) {
	if r.Intents == nil || r.Jobs == nil || r.Sources == nil || r.Commit == nil || r.Graph == nil {
		return nil, ErrRecoveryRequired
	}
	intents, err := r.Intents.ListNonterminalReleaseIntents(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]RecoveryResult, 0, len(intents))
	for _, intent := range intents {
		result := r.reconcile(ctx, intent)
		results = append(results, result)
	}
	return results, nil
}

func (r IntentRecovery) reconcile(ctx context.Context, intent Intent) RecoveryResult {
	result := RecoveryResult{IntentID: intent.ID}
	job, err := r.Jobs.GetReleaseJob(ctx, intent.JobID)
	if err != nil || !job.Valid() || job.ID != intent.JobID || job.RevisionID != intent.CandidateRevisionID || job.RequestHash != intent.RequestHash {
		return r.fail(ctx, intent, job, false, fmt.Errorf("%w: intent/job identity cannot be verified", ErrRecoveryRequired))
	}
	pointer, err := r.Sources.GetActivePointer(ctx)
	if err != nil || !pointer.Valid() {
		return r.fail(ctx, intent, job, false, fmt.Errorf("%w: active pointer cannot be read", ErrRecoveryRequired))
	}
	if pointer.ReleaseID.Valid() {
		active, releaseErr := r.Sources.GetRelease(ctx, pointer.ReleaseID)
		if releaseErr != nil {
			return r.fail(ctx, intent, job, false, fmt.Errorf("%w: active release cannot be read", ErrRecoveryRequired))
		}
		if active.IntentID == intent.ID {
			if intent.Phase != IntentSucceeded {
				if _, swapped, transitionErr := r.Intents.TransitionIntent(ctx, intent.ID, intent.Phase, IntentSucceeded, intent.ExternalTaskID, ""); transitionErr != nil || !swapped {
					return RecoveryResult{IntentID: intent.ID, Outcome: RecoveryRequiresSupport, Err: fmt.Errorf("%w: existing release intent could not be finalized", ErrRecoveryRequired)}
				}
			}
			if completeErr := r.completeJob(ctx, job, active); completeErr != nil {
				return RecoveryResult{IntentID: intent.ID, Outcome: RecoveryRequiresSupport, Err: completeErr}
			}
			return RecoveryResult{IntentID: intent.ID, Outcome: RecoveryCompleted}
		}
	}

	graphRead := GraphReadRequest{ProjectID: job.ProjectID, RevisionID: intent.CandidateRevisionID, ConfigHash: job.InputHash}
	snapshot, snapshotErr := r.Graph.ActiveSnapshot(ctx, graphRead)
	if snapshotErr != nil {
		return r.fail(ctx, intent, job, false, fmt.Errorf("%w: Graph active snapshot cannot be verified", ErrRecoveryRequired))
	}
	graphMatchesCandidate := snapshot.MatchesRead(graphRead)
	if pointer.ReleaseID != intent.BaselineReleaseID {
		return r.fail(ctx, intent, job, graphMatchesCandidate, fmt.Errorf("%w: active baseline changed", ErrRecoveryRequired))
	}

	switch intent.Phase {
	case IntentRecorded:
		advanced, swapped, transitionErr := r.Intents.TransitionIntent(ctx, intent.ID, IntentRecorded, IntentGraphActivating, intent.ExternalTaskID, "")
		if transitionErr != nil || !swapped {
			return RecoveryResult{IntentID: intent.ID, Outcome: RecoveryRequiresSupport, Err: fmt.Errorf("%w: could not claim Graph activation", ErrRecoveryRequired)}
		}
		intent = advanced
	case IntentGraphActivating:
	case IntentGraphActivated, IntentPointerCommitting:
		if !graphMatchesCandidate {
			return r.fail(ctx, intent, job, false, fmt.Errorf("%w: Graph is not the candidate snapshot", ErrRecoveryRequired))
		}
	default:
		return r.fail(ctx, intent, job, graphMatchesCandidate, fmt.Errorf("%w: phase %s cannot be replayed", ErrRecoveryRequired, intent.Phase))
	}

	if intent.Phase == IntentGraphActivating {
		if !graphMatchesCandidate {
			if _, activationErr := ActivateGraph(ctx, r.Graph, job, intent); activationErr != nil {
				// A transport/process failure may have happened after Graph accepted
				// the idempotent request. Read the explicitly scoped snapshot once
				// before declaring failure so an accepted activation can continue.
				observed, observeErr := r.Graph.ActiveSnapshot(ctx, graphRead)
				if observeErr != nil || !observed.MatchesRead(graphRead) {
					return r.fail(ctx, intent, job, false, fmt.Errorf("%w: Graph activation could not be verified", ErrRecoveryRequired))
				}
			}
		}
		advanced, swapped, transitionErr := r.Intents.TransitionIntent(ctx, intent.ID, IntentGraphActivating, IntentGraphActivated, intent.ExternalTaskID, "")
		if transitionErr != nil || !swapped {
			return RecoveryResult{IntentID: intent.ID, Outcome: RecoveryRequiresSupport, Err: fmt.Errorf("%w: Graph activation phase could not be persisted", ErrRecoveryRequired)}
		}
		intent = advanced
	}
	if intent.Phase == IntentGraphActivated {
		advanced, swapped, transitionErr := r.Intents.TransitionIntent(ctx, intent.ID, IntentGraphActivated, IntentPointerCommitting, intent.ExternalTaskID, "")
		if transitionErr != nil || !swapped {
			return RecoveryResult{IntentID: intent.ID, Outcome: RecoveryRequiresSupport, Err: fmt.Errorf("%w: pointer commit could not be claimed", ErrRecoveryRequired)}
		}
		intent = advanced
	}
	release, _, commitErr := r.Commit.CommitActivatedIntent(ctx, intent.ID, pointer.Generation)
	if commitErr != nil {
		return r.fail(ctx, intent, job, true, fmt.Errorf("%w: release/pointer commit conflict", ErrRecoveryRequired))
	}
	if completeErr := r.completeJob(ctx, job, release); completeErr != nil {
		return RecoveryResult{IntentID: intent.ID, Outcome: RecoveryRequiresSupport, Err: completeErr}
	}
	result.Outcome = RecoveryContinued
	return result
}

func (r IntentRecovery) completeJob(ctx context.Context, job Job, release Release) error {
	if job.Status == JobSucceeded && job.Result != nil && job.Result.ID == release.ID {
		return nil
	}
	_, swapped, err := r.Jobs.TransitionReleaseJob(ctx, job.ID, job.Status, JobSucceeded, &JobResult{Type: "release", ID: release.ID, URL: "/api/v1/releases/" + string(release.ID)})
	if err != nil || !swapped {
		return fmt.Errorf("%w: release Job could not be completed", ErrRecoveryRequired)
	}
	return nil
}

func (r IntentRecovery) fail(ctx context.Context, intent Intent, job Job, restore bool, reason error) RecoveryResult {
	details := reason.Error()
	outcome := RecoveryFailed
	if restore {
		restoreRequest := GraphRestoreRequest{ProjectID: job.ProjectID, PreviousGraphIdentity: intent.PreviousGraphIdentity, PreviousReleaseID: intent.PreviousReleaseID, IntentID: intent.ID}
		if !restoreRequest.Valid() || r.Graph.Restore(ctx, restoreRequest) != nil {
			details = fmt.Sprintf("%s; previous Graph snapshot restoration required", details)
			outcome = RecoveryRequiresSupport
		}
	}
	if _, swapped, transitionErr := r.Intents.TransitionIntent(ctx, intent.ID, intent.Phase, IntentFailed, intent.ExternalTaskID, details); transitionErr != nil || !swapped {
		return RecoveryResult{IntentID: intent.ID, Outcome: RecoveryRequiresSupport, Err: fmt.Errorf("%w: %s", ErrRecoveryRequired, details)}
	}
	if job.Status == JobQueued || job.Status == JobRunning || job.Status == JobInterrupted {
		if _, swapped, transitionErr := r.Jobs.TransitionReleaseJob(ctx, job.ID, job.Status, JobFailed, nil); transitionErr != nil || !swapped {
			return RecoveryResult{IntentID: intent.ID, Outcome: RecoveryRequiresSupport, Err: fmt.Errorf("%w: failed Job could not be persisted", ErrRecoveryRequired)}
		}
	}
	return RecoveryResult{IntentID: intent.ID, Outcome: outcome, Err: reason}
}
