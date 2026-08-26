package app

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	"github.com/zouyi/eco-guardian/internal/graph/impact/analysis"
	"github.com/zouyi/eco-guardian/internal/graph/impact/orchestration"
	"github.com/zouyi/eco-guardian/internal/graph/impact/planner"
	impactreport "github.com/zouyi/eco-guardian/internal/graph/impact/report"
	"github.com/zouyi/eco-guardian/internal/graph/impact/retrieval"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

var ErrImpactApplicationUnavailable = errors.New("impact analysis application unavailable")

// ImpactAnalysisService is the transport-neutral application boundary used by
// HTTP and future desktop adapters. Completed reports remain immutable; path
// expansions and explanations are insert-only child resources.
type ImpactAnalysisService interface {
	CreateImpactAnalysis(context.Context, impact.Command, string, string) (sharedjob.Record, bool, error)
	GetImpactAnalysis(context.Context, domain.ID, string) (ImpactReportRead, error)
	ExpandImpactPaths(context.Context, domain.ID, string, int, string) (ImpactPathExpansion, error)
	ExplainImpactEvidence(context.Context, domain.ID, []string) (impact.ExplanationAttempt, error)
	GetImpactJob(context.Context, domain.ID) (sharedjob.Record, error)
	CancelImpactJob(context.Context, domain.ID) (sharedjob.Record, bool, error)
	ListImpactJobEvents(context.Context, domain.ID, int64) ([]sharedjob.Event, error)
}

type ImpactReportRead struct {
	Report    impact.Report
	Freshness impactreport.Freshness
	JobID     domain.ID
	CacheHit  bool
}

type ImpactPathExpansion struct {
	ReportID          domain.ID
	ExpansionHash     string
	TargetNodeID      string
	Paths             []impact.Path
	TruncationReasons []impact.TruncationReason
	Warnings          []string
	Replay            bool
}

type ImpactAnalysisApplication struct {
	Admission *planner.Admission
	Provider  impact.GraphQueryProvider
	Reports   impact.ReportStore
	Jobs      impact.JobStore
	Events    impact.EventStore
	IDs       impact.IDGenerator
	Clock     impact.Clock
	Explainer impact.EvidenceExplainer
	Submit    func(context.Context, domain.ID) error
}

func (a ImpactAnalysisApplication) CreateImpactAnalysis(ctx context.Context, command impact.Command, key, requestID string) (sharedjob.Record, bool, error) {
	if a.Admission == nil || a.Reports == nil || a.Jobs == nil || a.Clock == nil {
		return sharedjob.Record{}, false, ErrImpactApplicationUnavailable
	}
	input, _, inputHash, err := a.Admission.Explicit(ctx, command, requestID+".admission")
	if err != nil {
		return sharedjob.Record{}, false, err
	}
	job, replay, err := (orchestration.Submitter{Jobs: a.Jobs, Events: a.Events, Reports: a.Reports, Clock: a.Clock}).Submit(ctx, input, inputHash, key, "manual")
	if err != nil || replay || job.Status == sharedjob.Succeeded || a.Submit == nil {
		return job, replay, err
	}
	if err = a.Submit(ctx, job.ID); err != nil {
		return sharedjob.Record{}, false, err
	}
	return job, false, nil
}

func (a ImpactAnalysisApplication) GetImpactAnalysis(ctx context.Context, reportID domain.ID, requestID string) (ImpactReportRead, error) {
	if a.Reports == nil {
		return ImpactReportRead{}, ErrImpactApplicationUnavailable
	}
	saved, err := a.Reports.GetImpactReport(ctx, reportID)
	if err != nil {
		return ImpactReportRead{}, err
	}
	var current *impact.Input
	if a.Admission != nil {
		command := impact.Command{ProjectID: saved.Input.ProjectID, BaseRevisionID: saved.Input.Base.RevisionID, TargetRevisionID: saved.Input.Target.RevisionID, Filters: saved.Input.Filters, Limits: saved.Input.Limits, Suspected: saved.Input.Suspected}
		if value, _, _, admitErr := a.Admission.Explicit(ctx, command, requestID+".freshness"); admitErr == nil {
			current = &value
		}
	}
	read := ImpactReportRead{Report: saved, Freshness: impactreport.EvaluateFreshness(saved.Input, current)}
	if lookup, ok := a.Reports.(interface {
		ImpactJobForReport(context.Context, domain.ID) (sharedjob.Record, bool, error)
	}); ok {
		job, found, lookupErr := lookup.ImpactJobForReport(ctx, reportID)
		if lookupErr != nil {
			return ImpactReportRead{}, lookupErr
		}
		if found {
			read.JobID = job.ID
			read.CacheHit = job.InputHash == saved.InputHash && job.CreatedAt.After(saved.CreatedAt)
		}
	}
	return read, nil
}

func (a ImpactAnalysisApplication) ExpandImpactPaths(ctx context.Context, reportID domain.ID, targetNodeID string, maxPaths int, requestID string) (ImpactPathExpansion, error) {
	if a.Provider == nil || a.Reports == nil {
		return ImpactPathExpansion{}, ErrImpactApplicationUnavailable
	}
	report, err := a.Reports.GetImpactReport(ctx, reportID)
	if err != nil {
		return ImpactPathExpansion{}, err
	}
	if maxPaths == 0 {
		maxPaths = report.Input.Limits.ExpandedMaxPaths
	}
	eligible := false
	for _, affected := range report.Affected {
		if affected.Node.ID == targetNodeID {
			eligible = true
			break
		}
	}
	if !eligible {
		return ImpactPathExpansion{}, analysis.ErrProviderContract
	}
	hash, err := impactreport.ExpansionHash(string(reportID), targetNodeID, maxPaths, report.Input)
	if err != nil {
		return ImpactPathExpansion{}, err
	}
	paths, reasons, warnings, err := analysis.ExpandPaths(ctx, a.Provider, report.Input, report.Changed, targetNodeID, maxPaths, requestID+".paths")
	if err != nil {
		return ImpactPathExpansion{}, err
	}
	replay, err := a.Reports.ExpandPaths(ctx, reportID, hash, targetNodeID, paths)
	return ImpactPathExpansion{ReportID: reportID, ExpansionHash: hash, TargetNodeID: targetNodeID, Paths: paths, TruncationReasons: reasons, Warnings: warnings, Replay: replay}, err
}

func (a ImpactAnalysisApplication) ExplainImpactEvidence(ctx context.Context, reportID domain.ID, requested []string) (impact.ExplanationAttempt, error) {
	if a.Reports == nil || a.IDs == nil || a.Clock == nil || a.Explainer == nil {
		return impact.ExplanationAttempt{}, ErrImpactApplicationUnavailable
	}
	report, err := a.Reports.GetImpactReport(ctx, reportID)
	if err != nil {
		return impact.ExplanationAttempt{}, err
	}
	catalog, err := impactEvidenceCatalog(report)
	if err != nil {
		return impact.ExplanationAttempt{}, err
	}
	refs := make([]impact.EvidenceRef, 0, len(requested))
	seen := map[string]struct{}{}
	for _, id := range requested {
		ref, found := catalog[id]
		if !found {
			return impact.ExplanationAttempt{}, retrieval.ErrInvalidExplanation
		}
		if _, duplicate := seen[id]; duplicate {
			return impact.ExplanationAttempt{}, retrieval.ErrInvalidExplanation
		}
		seen[id] = struct{}{}
		refs = append(refs, ref)
	}
	_, inputHash, err := retrieval.ExplanationInputHash(reportID, refs)
	if err != nil {
		return impact.ExplanationAttempt{}, err
	}
	if existing, found, findErr := a.Reports.FindExplanation(ctx, reportID, inputHash); findErr != nil {
		return impact.ExplanationAttempt{}, findErr
	} else if found {
		return existing, nil
	}
	attempt, err := retrieval.Explain(ctx, a.Explainer, a.IDs, a.Clock, reportID, refs)
	if err != nil {
		return impact.ExplanationAttempt{}, err
	}
	if err = a.Reports.SaveExplanation(ctx, reportID, attempt); err != nil {
		return impact.ExplanationAttempt{}, err
	}
	return attempt, nil
}

func (a ImpactAnalysisApplication) GetImpactJob(ctx context.Context, id domain.ID) (sharedjob.Record, error) {
	if a.Jobs == nil {
		return sharedjob.Record{}, ErrImpactApplicationUnavailable
	}
	job, err := a.Jobs.GetJob(ctx, id)
	if err == nil && job.Kind != orchestration.JobKind {
		return sharedjob.Record{}, ErrImpactApplicationUnavailable
	}
	return job, err
}

func (a ImpactAnalysisApplication) CancelImpactJob(ctx context.Context, id domain.ID) (sharedjob.Record, bool, error) {
	if _, err := a.GetImpactJob(ctx, id); err != nil {
		return sharedjob.Record{}, false, err
	}
	return a.Jobs.RequestCancellation(ctx, id)
}

func (a ImpactAnalysisApplication) ListImpactJobEvents(ctx context.Context, id domain.ID, after int64) ([]sharedjob.Event, error) {
	if a.Events == nil {
		return nil, ErrImpactApplicationUnavailable
	}
	if _, err := a.GetImpactJob(ctx, id); err != nil {
		return nil, err
	}
	return a.Events.ListEvents(ctx, id, after)
}

func impactEvidenceCatalog(report impact.Report) (map[string]impact.EvidenceRef, error) {
	result := map[string]impact.EvidenceRef{}
	add := func(kind string, value any) error {
		id, err := impact.EvidenceID(kind, value)
		if err != nil {
			return err
		}
		result[id] = impact.EvidenceRef{ID: id, Hash: id, Kind: kind}
		return nil
	}
	for _, affected := range report.Affected {
		if err := add("node", affected.Node); err != nil {
			return nil, err
		}
		if affected.DefaultPath != nil {
			if err := add("path", *affected.DefaultPath); err != nil {
				return nil, err
			}
		}
	}
	for _, suspected := range report.Suspected {
		if err := add("suspected", suspected); err != nil {
			return nil, err
		}
	}
	return result, nil
}
