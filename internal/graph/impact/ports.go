package impact

import (
	"context"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type RevisionReader interface {
	ReadProjectionRevision(context.Context, domain.ID) (projector.Revision, error)
	GetRevisionRecord(context.Context, domain.ID) (versioningrevision.Record, error)
}

type DiffReader interface{ versioningdiff.ManifestReader }
type BaselineReader interface {
	versioningdiff.ActiveBaselineReader
}

type ValidationGate interface {
	Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error)
}

type ProjectionSummaryReader interface {
	GraphProjectionSummary(context.Context, domain.ID) (projector.Summary, bool, error)
}

type GraphReadinessReader interface {
	InspectSnapshot(context.Context, string, string, string) (graphsync.Snapshot, error)
}

type GraphQueryProvider interface {
	Traverse(context.Context, TraverseRequest, string) (TraverseResponse, error)
	Paths(context.Context, PathsRequest, string) (PathsResponse, error)
	Retrieve(context.Context, RetrieveRequest, string) (RetrieveResponse, error)
}

type ReportStore interface {
	CreateOrGetStaging(context.Context, domain.ID, Input, string) (domain.ID, bool, error)
	LoadStaging(context.Context, domain.ID) (domain.ID, Input, string, string, bool, error)
	SaveChanged(context.Context, domain.ID, []ChangedEntity) error
	SaveTraversal(context.Context, domain.ID, []AffectedEntity, []TruncationReason, []string) error
	SaveDefaultPaths(context.Context, domain.ID, []AffectedEntity) error
	SaveSuspected(context.Context, domain.ID, SuspectedState, []SuspectedEvidence, []string) error
	Seal(context.Context, Report) (Report, bool, error)
	GetImpactReport(context.Context, domain.ID) (Report, error)
	FindByInputHash(context.Context, string) (Report, bool, error)
	ExpandPaths(context.Context, domain.ID, string, string, []Path) (bool, error)
	SaveExplanation(context.Context, domain.ID, ExplanationAttempt) error
	FindExplanation(context.Context, domain.ID, string) (ExplanationAttempt, bool, error)
}

type CompletionStore interface {
	SealImpactSuccess(context.Context, Report, domain.ID, int64) (Report, bool, error)
}

type HandoffStateStore interface {
	SetImpactHandoffState(context.Context, domain.ID, string, string, domain.ID, domain.ID, domain.ID, string) error
}

type JobStore = sharedjob.Store
type EventStore = sharedjob.EventStore
type Clock = sharedjob.Clock
type IDGenerator = sharedjob.IDGenerator

type CancellationReader interface {
	GetJob(context.Context, domain.ID) (sharedjob.Record, error)
}

type EvidenceRef struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
	Kind string `json:"kind"`
}

type ExplanationInput struct {
	ReportID domain.ID
	Refs     []EvidenceRef
}

type ExplanationOutput struct {
	Text, Provider, Model string
	Refs                  []string
}

type EvidenceExplainer interface {
	Explain(context.Context, ExplanationInput) (ExplanationOutput, error)
}

type ExplanationAttempt struct {
	ID, ReportID domain.ID
	InputHash    string
	Status       string
	Text         string
	Provider     string
	Model        string
	Refs         []string
	Diagnostics  string
	CreatedAt    time.Time
}
