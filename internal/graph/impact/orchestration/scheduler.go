package orchestration

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	"github.com/zouyi/eco-guardian/internal/graph/impact/planner"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

type Scheduler struct {
	ProjectID domain.ID
	Baselines impact.BaselineReader
	Admission planner.Admission
	Submitter Submitter
	Handoffs  impact.HandoffStateStore
	Filters   impact.Filters
	Limits    impact.Limits
	Suspected impact.SuspectedOptions
	Start     func(context.Context, domain.ID) error
}

func (s Scheduler) EnqueueImpact(ctx context.Context, handoff graphsync.ImpactHandoff) error {
	if !handoff.Valid() || s.Handoffs == nil {
		return graphsync.ErrImpactHandoffInvalid
	}
	command, err := planner.AutomaticCommand(ctx, s.Baselines, s.ProjectID, handoff.RevisionID, s.Filters, s.Limits, s.Suspected)
	if errors.Is(err, planner.ErrNoBaseline) {
		return s.Handoffs.SetImpactHandoffState(ctx, handoff.RevisionID, handoff.GraphHash, "waiting", "", "", "", "NO_BASELINE")
	}
	if err != nil {
		return err
	}
	input, _, inputHash, err := s.Admission.Explicit(ctx, command, "impact-handoff-"+string(handoff.RevisionID))
	if err != nil {
		reason := "BASE_GRAPH_WAITING"
		if !errors.Is(err, planner.ErrGraphNotReady) && !errors.Is(err, planner.ErrGraphIdentity) {
			reason = "IMPACT_ADMISSION_FAILED"
		}
		return s.Handoffs.SetImpactHandoffState(ctx, handoff.RevisionID, handoff.GraphHash, "waiting", command.BaseRevisionID, "", "", reason)
	}
	if input.Target.GraphManifestHash != handoff.GraphHash {
		return s.Handoffs.SetImpactHandoffState(ctx, handoff.RevisionID, handoff.GraphHash, "failed", command.BaseRevisionID, "", "", "GRAPH_IDENTITY_MISMATCH")
	}
	job, _, err := s.Submitter.Submit(ctx, input, inputHash, "", "automatic")
	if err != nil {
		return err
	}
	if err = s.Handoffs.SetImpactHandoffState(ctx, handoff.RevisionID, handoff.GraphHash, "claimed", command.BaseRevisionID, job.ID, "", ""); err != nil {
		return err
	}
	if s.Start != nil && job.Status != "succeeded" {
		return s.Start(ctx, job.ID)
	}
	return nil
}

var _ graphsync.ImpactScheduler = Scheduler{}
