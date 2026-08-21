package app

import (
	"context"
	"errors"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var (
	ErrGraphOperationUnavailable = errors.New("graph operation is unavailable")
	ErrGraphRevisionUnavailable  = errors.New("graph revision is unavailable")
	ErrGraphRetryInvalid         = errors.New("graph retry is invalid")
)

// GraphSyncService is the small application boundary for explicit Graph
// admission. It deliberately exposes neither a provider nor a SQLite handle
// to HTTP transports.
type GraphSyncService interface {
	EnsureGraphSync(context.Context, domain.ID) (graphsync.GraphJob, bool, error)
	RetryGraphSync(context.Context, domain.ID, domain.ID, string) (graphsync.GraphJob, bool, error)
	GraphStatus(context.Context, domain.ID) (GraphStatus, error)
}

// GraphStatus is the application-level exact-revision read model shared by
// HTTP and future UI adapters. It contains no provider payload and its read
// path has no side effects.
type GraphStatus struct {
	RevisionID, ConfigHash string
	Pipeline               graphsync.PipelineState
	HasSyncState           bool
	Freshness              graphsync.Freshness
	Validation             validation.GateResult
	Summary                *projector.Summary
	Job                    *graphsync.GraphJob
	Warnings               []string
	SafeError              string
	ExternalTaskID         string
	ImpactState            string
	Provider               *graphsync.Snapshot
	ProviderError          *graphsync.ProviderError
	ProviderObservedAt     time.Time
}

type graphRevisionSource interface {
	ProjectID() domain.ID
	GetRevisionRecord(context.Context, domain.ID) (versioningrevision.Record, error)
}

type graphJobSource interface {
	graphsync.JobAdmission
	GetGraphJob(context.Context, domain.ID) (graphsync.GraphJob, error)
}

type graphStateSource interface {
	GetGraphSyncState(context.Context, domain.ID) (graphsync.SyncState, bool, error)
}

type graphSummarySource interface {
	GraphProjectionSummary(context.Context, domain.ID) (projector.Summary, bool, error)
}

type graphImpactSource interface {
	GraphImpactHandoffStatus(context.Context, domain.ID, string) (string, bool, error)
}

// GraphSyncApplication composes existing immutable revision, validation, and
// durable Job seams. It does not run a provider effect; worker ownership stays
// in the composition root.
type GraphSyncApplication struct {
	Revisions  graphRevisionSource
	Jobs       graphJobSource
	States     graphStateSource
	Summaries  graphSummarySource
	Impact     graphImpactSource
	Validation graphsync.FullValidationGate
	Provider   graphsync.GraphProvider
	Clock      func() time.Time
	Submit     func(context.Context, graphsync.GraphJob) error
}

func (s GraphSyncApplication) GraphStatus(ctx context.Context, revisionID domain.ID) (GraphStatus, error) {
	record, versions, err := s.revision(ctx, revisionID)
	if err != nil {
		return GraphStatus{}, err
	}
	if s.States == nil || s.Summaries == nil || s.Jobs == nil || s.Validation == nil {
		return GraphStatus{}, ErrGraphOperationUnavailable
	}
	status := GraphStatus{RevisionID: string(revisionID), ConfigHash: record.Metadata.ConfigHash, Pipeline: graphsync.StateSaved, Warnings: []string{}}
	if result, checkErr := s.Validation.Check(ctx, revisionID, record.Metadata.ConfigHash, versions); checkErr == nil {
		status.Validation = result
	} else {
		status.Validation = validation.GateRequiresValidation
	}
	state, found, err := s.States.GetGraphSyncState(ctx, revisionID)
	if err != nil {
		return GraphStatus{}, ErrGraphOperationUnavailable
	}
	if !found {
		status.Freshness = graphsync.Freshness{Reasons: []string{"MISSING_GRAPH_STATE"}}
		return status, nil
	}
	status.HasSyncState = true
	status.Pipeline, status.Warnings, status.SafeError, status.ExternalTaskID = state.Pipeline, append([]string(nil), state.Warnings...), state.SafeError, state.ExternalTaskID
	if state.LatestJobID != "" {
		job, jobErr := s.Jobs.GetGraphJob(ctx, domain.ID(state.LatestJobID))
		if jobErr == nil {
			status.Job = &job
		}
	}
	if summary, summaryFound, summaryErr := s.Summaries.GraphProjectionSummary(ctx, revisionID); summaryErr == nil && summaryFound {
		status.Summary = &summary
	} else if summaryErr != nil {
		return GraphStatus{}, ErrGraphOperationUnavailable
	}
	if status.Summary != nil && s.Impact != nil {
		impact, impactFound, impactErr := s.Impact.GraphImpactHandoffStatus(ctx, revisionID, status.Summary.ManifestHash)
		if impactErr != nil {
			return GraphStatus{}, ErrGraphOperationUnavailable
		}
		if impactFound {
			status.ImpactState = impact
		} else {
			status.ImpactState = "unavailable"
		}
	}
	status.Freshness = graphsync.ComputeFreshness(graphsync.FreshnessInput{RevisionID: revisionID, InputHash: record.Metadata.ConfigHash, State: state, Job: status.Job})
	if s.Provider != nil {
		observedAt := time.Now().UTC()
		if s.Clock != nil {
			observedAt = s.Clock().UTC()
		}
		snapshot, inspectErr := s.Provider.InspectSnapshot(ctx, string(s.Revisions.ProjectID()), string(revisionID), "graph-status-"+string(revisionID))
		status.ProviderObservedAt = observedAt
		if inspectErr != nil {
			if providerErr, ok := inspectErr.(*graphsync.ProviderError); ok {
				status.ProviderError = providerErr
			} else {
				status.ProviderError = &graphsync.ProviderError{Code: "GRAPH_STATUS_UNAVAILABLE", Message: "Graph provider inspection is unavailable", RequestID: "graph-status-" + string(revisionID), Details: map[string]any{}}
			}
		} else if snapshot.Namespace != string(s.Revisions.ProjectID()) || snapshot.Version != string(revisionID) {
			status.ProviderError = &graphsync.ProviderError{Code: "GRAPH_STATUS_UNAVAILABLE", Message: "Graph provider returned a different Snapshot identity", RequestID: "graph-status-" + string(revisionID), Details: map[string]any{}}
		} else {
			status.Provider = &snapshot
		}
	}
	return status, nil
}

func (s GraphSyncApplication) EnsureGraphSync(ctx context.Context, revisionID domain.ID) (graphsync.GraphJob, bool, error) {
	record, versions, err := s.revision(ctx, revisionID)
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	if s.Jobs == nil || s.Validation == nil {
		return graphsync.GraphJob{}, false, ErrGraphOperationUnavailable
	}
	job, replay, err := (graphsync.AdmissionService{Validation: s.Validation, Jobs: s.Jobs}).Admit(ctx, graphsync.AutomaticGraphJobRequest(s.Revisions.ProjectID(), revisionID, record.Metadata.ConfigHash, versions, nil), versions)
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	if !replay && s.Submit != nil {
		if err = s.Submit(ctx, job); err != nil {
			return graphsync.GraphJob{}, false, err
		}
	}
	return job, replay, nil
}

func (s GraphSyncApplication) RetryGraphSync(ctx context.Context, revisionID, retryOfJobID domain.ID, key string) (graphsync.GraphJob, bool, error) {
	_, versions, err := s.revision(ctx, revisionID)
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	if s.Jobs == nil || s.Validation == nil || !retryOfJobID.Valid() || key == "" {
		return graphsync.GraphJob{}, false, ErrGraphOperationUnavailable
	}
	previous, err := s.Jobs.GetGraphJob(ctx, retryOfJobID)
	if err != nil || previous.RevisionID != revisionID || !previous.Status.Terminal() {
		return graphsync.GraphJob{}, false, ErrGraphRetryInvalid
	}
	job, replay, err := (graphsync.AdmissionService{Validation: s.Validation, Jobs: s.Jobs}).AdmitRetry(ctx, previous, key, versions)
	if err != nil {
		return graphsync.GraphJob{}, false, err
	}
	if !replay && s.Submit != nil {
		if err = s.Submit(ctx, job); err != nil {
			return graphsync.GraphJob{}, false, err
		}
	}
	return job, replay, nil
}

func (s GraphSyncApplication) revision(ctx context.Context, revisionID domain.ID) (versioningrevision.Record, validation.VersionManifest, error) {
	if s.Revisions == nil || !revisionID.Valid() {
		return versioningrevision.Record{}, validation.VersionManifest{}, ErrGraphRevisionUnavailable
	}
	record, err := s.Revisions.GetRevisionRecord(ctx, revisionID)
	if err != nil || record.Metadata.RevisionID != revisionID || !record.Valid() {
		return versioningrevision.Record{}, validation.VersionManifest{}, ErrGraphRevisionUnavailable
	}
	versions, ok := graphValidationVersions(record.Metadata.Manifest)
	if !ok {
		return versioningrevision.Record{}, validation.VersionManifest{}, ErrGraphOperationUnavailable
	}
	return record, versions, nil
}

func graphValidationVersions(manifest versioningrevision.VersionManifest) (validation.VersionManifest, bool) {
	values := map[string]string{}
	for _, entry := range manifest.Entries {
		if entry.State == versioningrevision.Registered {
			values[entry.CapabilityID] = entry.ImplementationVersion
		}
	}
	versions := validation.VersionManifest{Schema: values["schema"], DSL: values["dsl"], Registry: values["validator-registry"], NumericPolicy: values["numeric-policy"]}
	return versions, versions.Valid()
}

var _ GraphSyncService = GraphSyncApplication{}
