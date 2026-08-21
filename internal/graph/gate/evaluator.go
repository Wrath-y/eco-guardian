package gate

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type EvaluationRequest struct {
	Candidate versioningrevision.CandidateContext
	Summary   projector.Summary
	State     graphsync.SyncState
	Job       *graphsync.GraphJob
	RequestID string
}

type Evaluation struct {
	State    versioninggate.ResultState
	Evidence []versioninggate.Evidence
	Warnings []string
	Reasons  []string
}

type Evaluator struct{ Provider graphsync.GraphProvider }

// Evaluate always re-inspects the explicit Snapshot identity. It never trusts
// a local graph_ready flag by itself and never invokes activation or mutation.
func (e Evaluator) Evaluate(ctx context.Context, request EvaluationRequest) Evaluation {
	if e.Provider == nil || !request.Candidate.Valid() || !request.Summary.Valid() || request.RequestID == "" {
		return Evaluation{State: versioninggate.Unavailable, Reasons: []string{"GRAPH_GATE_INPUT_UNAVAILABLE"}}
	}
	if string(request.Candidate.RevisionID) != request.Summary.RevisionID || request.Candidate.ConfigHash != request.Summary.ConfigHash || request.State.RevisionID != request.Summary.RevisionID || request.State.Pipeline != graphsync.StateReady || request.State.LatestJobID == "" || request.Job == nil || string(request.Job.ID) != request.State.LatestJobID || request.Job.RevisionID != request.Candidate.RevisionID || request.Job.InputHash != request.Candidate.ConfigHash || request.Job.Status != graphsync.JobSucceeded {
		return Evaluation{State: versioninggate.Stale, Reasons: []string{"LOCAL_GRAPH_IDENTITY_MISMATCH"}}
	}
	snapshot, err := e.Provider.InspectSnapshot(ctx, request.Summary.ProjectID, request.Summary.RevisionID, request.RequestID)
	if err != nil {
		return Evaluation{State: versioninggate.Unavailable, Reasons: []string{"PROVIDER_SNAPSHOT_UNAVAILABLE"}}
	}
	verification := graphsync.VerifySnapshot(graphsync.SnapshotExpectation{Namespace: request.Summary.ProjectID, Version: request.Summary.RevisionID, ContentHash: request.Summary.ManifestHash, NodeCount: request.Summary.NodeCount, EdgeCount: request.Summary.EdgeCount}, snapshot)
	if !verification.Ready {
		state := versioninggate.Block
		for _, reason := range verification.Reasons {
			if reason == "SNAPSHOT_IDENTITY_MISMATCH" || reason == "CONTENT_HASH_MISMATCH" || reason == "SNAPSHOT_COUNT_MISMATCH" {
				state = versioninggate.Stale
				break
			}
		}
		return Evaluation{State: state, Reasons: append([]string(nil), verification.Reasons...)}
	}
	return Evaluation{State: versioninggate.Pass, Evidence: []versioninggate.Evidence{{ID: "graph-summary:" + request.Summary.RevisionID, Hash: request.Summary.ManifestHash}, {ID: "provider-snapshot:" + request.Summary.ProjectID + ":" + request.Summary.RevisionID, Hash: snapshot.ContentHash}}, Warnings: append([]string(nil), verification.Warnings...)}
}
