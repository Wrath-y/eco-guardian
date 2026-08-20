package sync

import (
	"context"
	"errors"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
)

var ErrSubmissionInvalid = errors.New("graph snapshot submission is invalid")

// SubmissionService persists the provider identity before a Job can advance
// beyond TASK_ACCEPTED. Replays reuse that identity and never issue a second
// PUT from the same logical Job.
type SubmissionService struct {
	States   SyncStateStore
	Worker   PhaseWorker
	Provider GraphProvider
}

type SubmissionRequest struct {
	JobID, RevisionID  domain.ID
	Namespace, Version string
	Snapshot           PutSnapshotRequest
	RequestID          string
}

func (s SubmissionService) Submit(ctx context.Context, request SubmissionRequest) (SyncState, bool, error) {
	if s.States == nil || s.Provider == nil || !request.JobID.Valid() || !request.RevisionID.Valid() || strings.TrimSpace(request.Namespace) == "" || strings.TrimSpace(request.Version) == "" || strings.TrimSpace(request.RequestID) == "" || request.Snapshot.SchemaVersion != SnapshotSchemaVersion || !validHash(request.Snapshot.ContentHash) {
		return SyncState{}, false, ErrSubmissionInvalid
	}
	state, found, err := s.States.GetGraphSyncState(ctx, request.RevisionID)
	if err != nil || !found || state.Pipeline != StateBuilding || state.LatestJobID != string(request.JobID) {
		return SyncState{}, false, ErrSubmissionInvalid
	}
	if state.ProviderTaskID != "" || state.ExternalTaskID != "" {
		if state.ProviderTaskID == "" || state.ExternalTaskID == "" || state.ProviderRequestID != request.RequestID {
			return SyncState{}, false, ErrSubmissionInvalid
		}
		if _, _, err = s.Worker.Checkpoint(ctx, request.JobID, PhaseTaskAccepted, 60, "", "", nil); err != nil {
			return SyncState{}, false, err
		}
		return state, true, nil
	}
	if _, _, err = s.Worker.Checkpoint(ctx, request.JobID, PhaseSubmitting, 50, "", "", nil); err != nil {
		return SyncState{}, false, err
	}
	snapshot, err := s.Provider.PutSnapshot(ctx, request.Namespace, request.Version, request.Snapshot, request.RequestID)
	if err != nil {
		return SyncState{}, false, err
	}
	if snapshot.Namespace != request.Namespace || snapshot.Version != request.Version || snapshot.ContentHash != request.Snapshot.ContentHash || strings.TrimSpace(snapshot.TaskID) == "" {
		return SyncState{}, false, ErrSubmissionInvalid
	}
	next := state
	next.Generation++
	next.ExternalTaskID, next.ProviderRequestID, next.ProviderTaskID = snapshot.TaskID, request.RequestID, snapshot.TaskID
	updated, swapped, err := s.States.CompareAndSwapGraphSyncState(ctx, state, next)
	if err != nil || !swapped {
		return SyncState{}, false, err
	}
	if _, _, err = s.Worker.Checkpoint(ctx, request.JobID, PhaseTaskAccepted, 60, "", "", nil); err != nil {
		return SyncState{}, false, err
	}
	return updated, false, nil
}
