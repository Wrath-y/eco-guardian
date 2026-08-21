package sync

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrReconciliationInvalid = errors.New("graph task reconciliation is invalid")

type ReconciliationDecision struct {
	AcceptExisting bool
	Resubmit       bool
	Verification   SnapshotVerification
}

type ReconciliationRequest struct {
	JobID, RevisionID  domain.ID
	Namespace, Version string
	Expectation        SnapshotExpectation
	Snapshot           PutSnapshotRequest
	RequestID          string
}

type ReconciliationService struct {
	States   SyncStateStore
	Worker   PhaseWorker
	Provider GraphProvider
}

// ReconcileMissingTask never guesses from a task error alone. A verifiable
// target Snapshot wins; only an absent target permits replaying the same PUT.
func ReconcileMissingTask(expected SnapshotExpectation, snapshot *Snapshot) (ReconciliationDecision, error) {
	if snapshot == nil {
		return ReconciliationDecision{Resubmit: true}, nil
	}
	verification := VerifySnapshot(expected, *snapshot)
	if verification.Ready {
		return ReconciliationDecision{AcceptExisting: true, Verification: verification}, nil
	}
	return ReconciliationDecision{Verification: verification}, ErrReconciliationInvalid
}

// ReconcileTaskNotFound handles only the provider's stable TASK_NOT_FOUND
// branch. An exact ready target Snapshot wins; a missing target is the sole
// condition that clears the old Task identity and replays the same immutable
// PUT. Any other inspection result is left untouched for explicit handling.
func (s ReconciliationService) ReconcileTaskNotFound(ctx context.Context, request ReconciliationRequest, taskErr error) (ReconciliationDecision, error) {
	if s.States == nil || s.Provider == nil || !request.JobID.Valid() || !request.RevisionID.Valid() || request.Namespace == "" || request.Version == "" || request.RequestID == "" || request.Expectation.Namespace != request.Namespace || request.Expectation.Version != request.Version || request.Expectation.ContentHash != request.Snapshot.ContentHash || request.Snapshot.SchemaVersion != SnapshotSchemaVersion || !isProviderErrorCode(taskErr, "TASK_NOT_FOUND") {
		return ReconciliationDecision{}, ErrReconciliationInvalid
	}
	state, found, err := s.States.GetGraphSyncState(ctx, request.RevisionID)
	if err != nil || !found || state.Pipeline != StateBuilding || state.LatestJobID != string(request.JobID) || state.ProviderTaskID == "" {
		return ReconciliationDecision{}, ErrReconciliationInvalid
	}
	snapshot, err := s.Provider.InspectSnapshot(ctx, request.Namespace, request.Version, request.RequestID)
	var target *Snapshot
	if err == nil {
		target = &snapshot
	} else if !isProviderErrorCode(err, "SNAPSHOT_NOT_FOUND") {
		return ReconciliationDecision{}, err
	}
	decision, err := ReconcileMissingTask(request.Expectation, target)
	if err != nil {
		return decision, err
	}
	if decision.AcceptExisting {
		if _, _, err = s.Worker.Checkpoint(ctx, request.JobID, PhaseVerifying, 85, "", "", nil); err != nil {
			return ReconciliationDecision{}, err
		}
		return decision, nil
	}
	next := state
	next.Generation++
	next.ExternalTaskID, next.ProviderTaskID = "", ""
	if _, swapped, err := s.States.CompareAndSwapGraphSyncState(ctx, state, next); err != nil || !swapped {
		return ReconciliationDecision{}, err
	}
	_, _, err = (SubmissionService{States: s.States, Worker: s.Worker, Provider: s.Provider}).Submit(ctx, SubmissionRequest{JobID: request.JobID, RevisionID: request.RevisionID, Namespace: request.Namespace, Version: request.Version, Snapshot: request.Snapshot, RequestID: request.RequestID, Resubmission: true})
	if err != nil {
		return ReconciliationDecision{}, err
	}
	return decision, nil
}

func isProviderErrorCode(err error, code string) bool {
	var provider *ProviderError
	return errors.As(err, &provider) && provider.Code == code
}
