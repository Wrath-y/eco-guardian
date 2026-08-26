package orchestration

import (
	"context"
	"sort"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	"github.com/zouyi/eco-guardian/internal/graph/impact/analysis"
	"github.com/zouyi/eco-guardian/internal/graph/impact/planner"
	impactreport "github.com/zouyi/eco-guardian/internal/graph/impact/report"
	"github.com/zouyi/eco-guardian/internal/graph/impact/retrieval"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type Worker struct {
	Admission   *planner.Admission
	Revisions   impact.RevisionReader
	Diffs       impact.DiffReader
	Provider    impact.GraphQueryProvider
	Reports     impact.ReportStore
	Jobs        impact.JobStore
	Events      impact.EventStore
	IDs         impact.IDGenerator
	Clock       impact.Clock
	Projector   projector.Descriptor
	PathOptions analysis.PathOptions
}

func (w Worker) Run(ctx context.Context, jobID domain.ID, requestID string) (impact.Report, error) {
	if w.Revisions == nil || w.Diffs == nil || w.Provider == nil || w.Reports == nil || w.Jobs == nil || w.IDs == nil || w.Clock == nil || !jobID.Valid() {
		return impact.Report{}, ErrSubmitInvalid
	}
	job, err := w.Jobs.GetJob(ctx, jobID)
	if err != nil || job.Kind != JobKind {
		return impact.Report{}, ErrSubmitInvalid
	}
	if job.Status == sharedjob.Succeeded && job.Result != nil {
		return w.Reports.GetImpactReport(ctx, job.Result.ID)
	}
	if job.Status == sharedjob.Queued {
		job, _, err = w.Jobs.Transition(ctx, job.ID, sharedjob.Queued, sharedjob.Running, nil, job.CancelGeneration)
		if err != nil {
			return impact.Report{}, err
		}
	} else if job.Status != sharedjob.Running && job.Status != sharedjob.Interrupted {
		return impact.Report{}, ErrSubmitInvalid
	}
	_, input, inputHash, _, found, err := w.Reports.LoadStaging(ctx, job.ID)
	if err != nil || !found || inputHash != job.InputHash {
		return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, ErrSubmitInvalid)
	}
	if canceled, cancelErr := w.cancelIfRequested(ctx, job.ID); cancelErr != nil || canceled {
		return impact.Report{}, cancelErr
	}
	if w.Admission != nil {
		admitted, _, admittedHash, admitErr := w.Admission.Explicit(ctx, impact.Command{ProjectID: input.ProjectID, BaseRevisionID: input.Base.RevisionID, TargetRevisionID: input.Target.RevisionID, Filters: input.Filters, Limits: input.Limits, Suspected: input.Suspected}, requestID+".admission")
		if admitErr != nil || admittedHash != inputHash || admitted.Target.GraphManifestHash != input.Target.GraphManifestHash || admitted.Base.GraphManifestHash != input.Base.GraphManifestHash {
			if admitErr == nil {
				admitErr = planner.ErrGraphIdentity
			}
			return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, admitErr)
		}
	}
	if err = appendPhase(ctx, w.Events, w.Clock, job.ID, PhaseAdmission, 5, "", "", nil); err != nil {
		return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
	}
	targetRevision, err := w.Revisions.ReadProjectionRevision(ctx, input.Target.RevisionID)
	if err != nil || targetRevision.ProjectID != input.ProjectID || targetRevision.ConfigHash != input.Target.ConfigHash {
		return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, planner.ErrInvalidRevisionPair)
	}
	changed, err := planner.ChangedSet(ctx, w.Diffs, input, targetRevision.Entities)
	if err != nil {
		return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
	}
	stagingID, _, _, _, _, _ := w.Reports.LoadStaging(ctx, job.ID)
	if err = w.Reports.SaveChanged(ctx, stagingID, changed); err != nil {
		return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
	}
	_ = appendPhase(ctx, w.Events, w.Clock, job.ID, PhaseDiff, 20, "", "", nil)
	if canceled, cancelErr := w.cancelIfRequested(ctx, job.ID); cancelErr != nil || canceled {
		return impact.Report{}, cancelErr
	}
	deterministic := analysis.DeterministicResult{State: "empty"}
	if len(changed) > 0 {
		deterministic, err = analysis.Traverse(ctx, w.Provider, input, changed, requestID+".traverse")
		if err != nil {
			return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
		}
	}
	if err = w.Reports.SaveTraversal(ctx, stagingID, deterministic.Affected, deterministic.Reasons, deterministic.Warnings); err != nil {
		return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
	}
	_ = appendPhase(ctx, w.Events, w.Clock, job.ID, PhaseTraversal, 45, "", "", nil)
	affected := deterministic.Affected
	pathReasons, pathWarnings := []impact.TruncationReason{}, []string{}
	if len(affected) > 0 {
		affected, pathReasons, pathWarnings, err = analysis.DefaultPaths(ctx, w.Provider, input, changed, affected, w.PathOptions, requestID+".paths")
		if err != nil {
			return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
		}
	}
	if err = w.Reports.SaveDefaultPaths(ctx, stagingID, affected); err != nil {
		return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
	}
	_ = appendPhase(ctx, w.Events, w.Clock, job.ID, PhaseDefaultPaths, 70, "", "", nil)
	if canceled, cancelErr := w.cancelIfRequested(ctx, job.ID); cancelErr != nil || canceled {
		return impact.Report{}, cancelErr
	}
	nodes := map[string]projector.Node{}
	if w.Projector.Valid() {
		if projected, projectErr := projector.Project(w.Projector, targetRevision); projectErr == nil {
			for _, node := range projected.Nodes {
				nodes[node.ID] = node
			}
		}
	}
	graphNodes := makeGraphNodes(nodes)
	suspected := retrieval.Result{State: impact.SuspectedDisabled}
	if len(changed) == 0 && input.Suspected.Enabled {
		suspected = retrieval.Result{State: impact.SuspectedUnavailable, Warnings: []string{"NO_CHANGED_ENTITIES"}}
	} else {
		suspected, err = retrieval.Retrieve(ctx, w.Provider, input, changed, graphNodes, requestID+".retrieve")
		if err != nil {
			return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
		}
	}
	if err = w.Reports.SaveSuspected(ctx, stagingID, suspected.State, suspected.Evidence, suspected.Warnings); err != nil {
		return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
	}
	_ = appendPhase(ctx, w.Events, w.Clock, job.ID, PhaseOptionalRetrieval, 85, "", "", nil)
	reasons := analysis.MergeReasons(deterministic.Reasons, pathReasons)
	warnings := mergeWarnings(deterministic.Warnings, pathWarnings, suspected.Warnings)
	for _, reason := range reasons {
		warnings = append(warnings, analysis.WarningFor(reason))
	}
	warnings = mergeWarnings(warnings)
	reportID, err := w.IDs.New()
	if err != nil {
		return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
	}
	mode := impact.ReverseDependencyImpact
	if input.Filters.Direction != impact.DirectionIncoming {
		mode = impact.RelationshipExploration
	}
	report := impact.Report{ID: reportID, Input: input, InputHash: inputHash, Mode: mode, Changed: changed, Affected: affected, Suspected: suspected.Evidence, SuspectedState: suspected.State, Truncated: len(reasons) > 0, Reasons: reasons, Warnings: warnings, CreatedAt: w.Clock.Now().UTC()}
	report.ResultHash, err = impactreport.ResultHash(report)
	if err != nil {
		return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
	}
	_ = appendPhase(ctx, w.Events, w.Clock, job.ID, PhaseVerification, 95, "", "", nil)
	current, err := w.Jobs.GetJob(ctx, job.ID)
	if err != nil || current.CancelGeneration != 0 {
		if err == nil {
			_, _, err = w.Jobs.Transition(ctx, current.ID, current.Status, sharedjob.Canceled, nil, current.CancelGeneration)
		}
		return impact.Report{}, err
	}
	if completion, ok := w.Reports.(impact.CompletionStore); ok {
		report, _, err = completion.SealImpactSuccess(ctx, report, job.ID, current.CancelGeneration)
	} else {
		report, _, err = w.Reports.Seal(ctx, report)
		if err == nil {
			_, _, err = w.Jobs.Transition(ctx, current.ID, current.Status, sharedjob.Succeeded, ResultLink(report.ID), current.CancelGeneration)
		}
	}
	if err != nil {
		return impact.Report{}, terminalFailure(ctx, w.Jobs, w.Events, w.Clock, job, err)
	}
	_ = appendPhase(ctx, w.Events, w.Clock, job.ID, PhaseFinalCommit, 100, "", "", ResultLink(report.ID))
	return report, nil
}

func (w Worker) cancelIfRequested(ctx context.Context, jobID domain.ID) (bool, error) {
	job, err := w.Jobs.GetJob(ctx, jobID)
	if err != nil {
		return false, err
	}
	if job.CancelGeneration == 0 {
		return false, nil
	}
	_, _, err = w.Jobs.Transition(ctx, job.ID, job.Status, sharedjob.Canceled, nil, job.CancelGeneration)
	return true, err
}

func makeGraphNodes(values map[string]projector.Node) map[string]graphsync.Node {
	result := make(map[string]graphsync.Node, len(values))
	for id, node := range values {
		result[id] = graphsync.Node{ID: node.ID, Type: node.Type, Label: node.Label, Text: node.Text, Properties: node.Properties, Provenance: map[string]any{"entity_id": node.Provenance.EntityID, "revision_id": node.Provenance.RevisionID}}
	}
	return result
}

func mergeWarnings(groups ...[]string) []string {
	set := map[string]struct{}{}
	for _, group := range groups {
		for _, value := range group {
			if value != "" {
				set[value] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
