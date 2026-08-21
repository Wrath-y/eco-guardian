package gate

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

var ErrActivationInvalid = errors.New("graph activation candidate is not ready")

type ProjectionSummaryReader interface {
	GraphProjectionSummary(context.Context, domain.ID) (projector.Summary, bool, error)
}

// ActivationAdapter is the narrow #7 activation port. It rechecks the exact
// candidate immediately before the provider call and has no release pointer
// or rollback responsibilities.
type ActivationAdapter struct {
	Provider  graphsync.GraphProvider
	Summaries ProjectionSummaryReader
}

func (a ActivationAdapter) Activate(ctx context.Context, request versioningrelease.GraphActivationRequest) (versioningrelease.GraphActivationEvidence, error) {
	if a.Provider == nil || a.Summaries == nil || !request.Valid() {
		return versioningrelease.GraphActivationEvidence{}, ErrActivationInvalid
	}
	summary, found, err := a.Summaries.GraphProjectionSummary(ctx, request.RevisionID)
	if err != nil || !found || !summary.Valid() || summary.ProjectID != string(request.ProjectID) || summary.RevisionID != string(request.RevisionID) || summary.ConfigHash != request.ConfigHash {
		return versioningrelease.GraphActivationEvidence{}, ErrActivationInvalid
	}
	snapshot, err := a.Provider.InspectSnapshot(ctx, summary.ProjectID, summary.RevisionID, string(request.IntentID))
	if err != nil || !graphsync.VerifySnapshot(graphsync.SnapshotExpectation{Namespace: summary.ProjectID, Version: summary.RevisionID, ContentHash: summary.ManifestHash, NodeCount: summary.NodeCount, EdgeCount: summary.EdgeCount}, snapshot).Ready {
		return versioningrelease.GraphActivationEvidence{}, ErrActivationInvalid
	}
	activation, err := a.Provider.ActivateSnapshot(ctx, summary.ProjectID, summary.RevisionID, string(request.IntentID))
	if err != nil || activation.Namespace != summary.ProjectID || activation.ActiveVersion != summary.RevisionID {
		return versioningrelease.GraphActivationEvidence{}, ErrActivationInvalid
	}
	return versioningrelease.GraphActivationEvidence{Changed: activation.Changed, ProjectID: request.ProjectID, RevisionID: request.RevisionID, ConfigHash: request.ConfigHash, IntentID: request.IntentID, TaskID: snapshot.TaskID}, nil
}
