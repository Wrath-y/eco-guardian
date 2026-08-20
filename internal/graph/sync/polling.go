package sync

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrPollingInvalid = errors.New("graph task polling is invalid")

type PollingService struct {
	States   SyncStateStore
	Worker   PhaseWorker
	Provider GraphProvider
}

type PollingRequest struct {
	JobID, RevisionID  domain.ID
	Namespace, Version string
	Expectation        SnapshotExpectation
	RequestID          string
}

type PollingResult struct {
	Task         Task
	Verification *SnapshotVerification
}

func (s PollingService) Poll(ctx context.Context, request PollingRequest) (PollingResult, error) {
	if s.States == nil || s.Provider == nil || !request.JobID.Valid() || !request.RevisionID.Valid() || request.Namespace == "" || request.Version == "" || request.RequestID == "" {
		return PollingResult{}, ErrPollingInvalid
	}
	state, found, err := s.States.GetGraphSyncState(ctx, request.RevisionID)
	if err != nil || !found || state.Pipeline != StateBuilding || state.LatestJobID != string(request.JobID) || state.ProviderTaskID == "" {
		return PollingResult{}, ErrPollingInvalid
	}
	if _, _, err = s.Worker.Checkpoint(ctx, request.JobID, PhasePolling, 70, "", "", nil); err != nil {
		return PollingResult{}, err
	}
	task, err := s.Provider.GetTask(ctx, state.ProviderTaskID, request.RequestID)
	if err != nil {
		return PollingResult{}, err
	}
	if task.ID != state.ProviderTaskID || task.Namespace != request.Namespace || task.SnapshotVersion != request.Version {
		return PollingResult{}, ErrPollingInvalid
	}
	switch task.State {
	case "queued", "running":
		return PollingResult{Task: task}, nil
	case "failed":
		return PollingResult{Task: task}, nil
	case "succeeded":
	default:
		return PollingResult{}, ErrPollingInvalid
	}
	if _, _, err = s.Worker.Checkpoint(ctx, request.JobID, PhaseVerifying, 85, "", "", nil); err != nil {
		return PollingResult{}, err
	}
	snapshot, err := s.Provider.InspectSnapshot(ctx, request.Namespace, request.Version, request.RequestID)
	if err != nil {
		return PollingResult{}, err
	}
	verification := VerifySnapshot(request.Expectation, snapshot)
	return PollingResult{Task: task, Verification: &verification}, nil
}
