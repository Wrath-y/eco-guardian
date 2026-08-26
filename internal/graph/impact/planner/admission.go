package planner

import (
	"context"
	"errors"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

var (
	ErrInvalidRevisionPair = errors.New("INVALID_REVISION_PAIR")
	ErrGraphNotReady       = errors.New("GRAPH_NOT_READY")
	ErrGraphIdentity       = errors.New("GRAPH_IDENTITY_MISMATCH")
	ErrValidationRequired  = errors.New("VALIDATION_REQUIRED")
	ErrNoBaseline          = errors.New("NO_BASELINE")
)

type Admission struct {
	Revisions  impact.RevisionReader
	Validation impact.ValidationGate
	Summaries  impact.ProjectionSummaryReader
	Graph      impact.GraphReadinessReader
}

func (a Admission) Explicit(ctx context.Context, command impact.Command, requestID string) (impact.Input, []byte, string, error) {
	if a.Revisions == nil || a.Validation == nil || a.Summaries == nil || a.Graph == nil || !command.ProjectID.Valid() || !command.BaseRevisionID.Valid() || !command.TargetRevisionID.Valid() || command.BaseRevisionID == command.TargetRevisionID {
		return impact.Input{}, nil, "", ErrInvalidRevisionPair
	}
	base, err := a.identity(ctx, command.ProjectID, command.BaseRevisionID, requestID+".base")
	if err != nil {
		return impact.Input{}, nil, "", err
	}
	target, err := a.identity(ctx, command.ProjectID, command.TargetRevisionID, requestID+".target")
	if err != nil {
		return impact.Input{}, nil, "", err
	}
	return Normalize(impact.Input{ProjectID: command.ProjectID, Base: base, Target: target, Filters: command.Filters, Limits: command.Limits, Suspected: command.Suspected})
}

func (a Admission) identity(ctx context.Context, projectID, revisionID domain.ID, requestID string) (impact.RevisionIdentity, error) {
	projection, err := a.Revisions.ReadProjectionRevision(ctx, revisionID)
	if err != nil || projection.ProjectID != projectID || projection.RevisionID != revisionID {
		return impact.RevisionIdentity{}, ErrInvalidRevisionPair
	}
	record, err := a.Revisions.GetRevisionRecord(ctx, revisionID)
	if err != nil || !record.Valid() || record.Metadata.RevisionID != revisionID || record.Metadata.ConfigHash != projection.ConfigHash {
		return impact.RevisionIdentity{}, ErrInvalidRevisionPair
	}
	versions, err := versioningrelease.ValidationManifest(record.Metadata.Manifest)
	if err != nil {
		return impact.RevisionIdentity{}, ErrValidationRequired
	}
	state, err := a.Validation.Check(ctx, revisionID, record.Metadata.ConfigHash, versions)
	if err != nil || state != validation.GatePass {
		return impact.RevisionIdentity{}, ErrValidationRequired
	}
	summary, found, err := a.Summaries.GraphProjectionSummary(ctx, revisionID)
	if err != nil || !found || !summary.Valid() || summary.ProjectID != string(projectID) || summary.RevisionID != string(revisionID) || summary.ConfigHash != record.Metadata.ConfigHash {
		return impact.RevisionIdentity{}, ErrGraphNotReady
	}
	snapshot, err := a.Graph.InspectSnapshot(ctx, string(projectID), string(revisionID), requestID)
	if err != nil {
		return impact.RevisionIdentity{}, fmt.Errorf("%w: %v", ErrGraphNotReady, err)
	}
	verification := graphsync.VerifySnapshot(graphsync.SnapshotExpectation{Namespace: string(projectID), Version: string(revisionID), ContentHash: summary.ManifestHash, NodeCount: summary.NodeCount, EdgeCount: summary.EdgeCount}, snapshot)
	if !verification.Ready {
		return impact.RevisionIdentity{}, ErrGraphIdentity
	}
	return impact.RevisionIdentity{RevisionID: revisionID, ConfigHash: record.Metadata.ConfigHash, VersionManifestHash: record.Metadata.ManifestHash, GraphManifestHash: summary.ManifestHash, GraphNodeCount: summary.NodeCount, GraphEdgeCount: summary.EdgeCount}, nil
}

func AutomaticCommand(ctx context.Context, baselines impact.BaselineReader, projectID, targetID domain.ID, filters impact.Filters, limits impact.Limits, suspected impact.SuspectedOptions) (impact.Command, error) {
	if baselines == nil || !projectID.Valid() || !targetID.Valid() {
		return impact.Command{}, ErrInvalidRevisionPair
	}
	baseID, found, err := baselines.ActiveBaseline(ctx)
	if err != nil {
		return impact.Command{}, err
	}
	if !found {
		return impact.Command{}, ErrNoBaseline
	}
	return impact.Command{ProjectID: projectID, BaseRevisionID: baseID, TargetRevisionID: targetID, Filters: filters, Limits: limits, Suspected: suspected}, nil
}
