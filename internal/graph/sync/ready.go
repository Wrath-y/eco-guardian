package sync

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
)

var ErrReadyCommitInvalid = errors.New("graph ready commit is invalid")

type ReadyCommitter interface {
	CommitGraphReady(context.Context, SyncState, projector.Summary, string) (SyncState, bool, error)
}

type ReadyService struct {
	States    SyncStateStore
	Worker    PhaseWorker
	Committer ReadyCommitter
}

// Commit accepts only a completed exact verification, then delegates the
// durable state/Job/result/handoff transaction to the storage seam.
func (s ReadyService) Commit(ctx context.Context, jobID, revisionID domain.ID, summary projector.Summary, evidence string, verification SnapshotVerification) (SyncState, bool, error) {
	if s.States == nil || s.Committer == nil || !jobID.Valid() || !revisionID.Valid() || !summary.Valid() || summary.RevisionID != string(revisionID) || evidence == "" || !verification.Ready {
		return SyncState{}, false, ErrReadyCommitInvalid
	}
	state, found, err := s.States.GetGraphSyncState(ctx, revisionID)
	if err != nil || !found || state.Pipeline != StateBuilding || state.LatestJobID != string(jobID) {
		return SyncState{}, false, ErrReadyCommitInvalid
	}
	state.Warnings = append([]string(nil), verification.Warnings...)
	ready, replay, err := s.Committer.CommitGraphReady(ctx, state, summary, evidence)
	if err != nil {
		return SyncState{}, false, err
	}
	if _, _, err = s.Worker.Checkpoint(ctx, jobID, PhaseReady, 95, "", "", nil); err != nil {
		return SyncState{}, false, err
	}
	if _, _, err = s.Worker.Checkpoint(ctx, jobID, PhaseImpactHandoffRecorded, 100, "", "", nil); err != nil {
		return SyncState{}, false, err
	}
	return ready, replay, nil
}
