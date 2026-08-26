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

type upstreamFake struct{}

func (upstreamFake) ReadProjectionRevision(context.Context, domain.ID) (projector.Revision, error) {
	return projector.Revision{}, nil
}
func (upstreamFake) GetRevisionRecord(context.Context, domain.ID) (versioningrevision.Record, error) {
	return versioningrevision.Record{}, nil
}
func (upstreamFake) Materialize(context.Context, domain.ID) ([]versioningdiff.EntityBlob, error) {
	return nil, nil
}
func (upstreamFake) ActiveBaseline(context.Context) (domain.ID, bool, error) { return "", false, nil }
func (upstreamFake) Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error) {
	return validation.GatePass, nil
}
func (upstreamFake) GraphProjectionSummary(context.Context, domain.ID) (projector.Summary, bool, error) {
	return projector.Summary{}, false, nil
}
func (upstreamFake) InspectSnapshot(context.Context, string, string, string) (graphsync.Snapshot, error) {
	return graphsync.Snapshot{}, nil
}

type reportFake struct{}

func (reportFake) CreateOrGetStaging(context.Context, domain.ID, Input, string) (domain.ID, bool, error) {
	return "", false, nil
}
func (reportFake) LoadStaging(context.Context, domain.ID) (domain.ID, Input, string, string, bool, error) {
	return "", Input{}, "", "", false, nil
}
func (reportFake) SaveChanged(context.Context, domain.ID, []ChangedEntity) error { return nil }
func (reportFake) SaveTraversal(context.Context, domain.ID, []AffectedEntity, []TruncationReason, []string) error {
	return nil
}
func (reportFake) SaveDefaultPaths(context.Context, domain.ID, []AffectedEntity) error { return nil }
func (reportFake) SaveSuspected(context.Context, domain.ID, SuspectedState, []SuspectedEvidence, []string) error {
	return nil
}
func (reportFake) Seal(_ context.Context, report Report) (Report, bool, error) {
	return report, false, nil
}
func (reportFake) GetImpactReport(context.Context, domain.ID) (Report, error) { return Report{}, nil }
func (reportFake) FindByInputHash(context.Context, string) (Report, bool, error) {
	return Report{}, false, nil
}
func (reportFake) ExpandPaths(context.Context, domain.ID, string, string, []Path) (bool, error) {
	return false, nil
}
func (reportFake) SaveExplanation(context.Context, domain.ID, ExplanationAttempt) error { return nil }
func (reportFake) FindExplanation(context.Context, domain.ID, string) (ExplanationAttempt, bool, error) {
	return ExplanationAttempt{}, false, nil
}

type jobFake struct{}

func (jobFake) CreateOrGet(context.Context, sharedjob.Request) (sharedjob.Record, bool, error) {
	return sharedjob.Record{}, false, nil
}
func (jobFake) GetJob(context.Context, domain.ID) (sharedjob.Record, error) {
	return sharedjob.Record{}, nil
}
func (jobFake) Transition(context.Context, domain.ID, sharedjob.Status, sharedjob.Status, *sharedjob.Result, int64) (sharedjob.Record, bool, error) {
	return sharedjob.Record{}, false, nil
}
func (jobFake) RequestCancellation(context.Context, domain.ID) (sharedjob.Record, bool, error) {
	return sharedjob.Record{}, false, nil
}

type eventFake struct{}

func (eventFake) Append(_ context.Context, event sharedjob.Event) (sharedjob.Event, bool, error) {
	return event, false, nil
}
func (eventFake) ListEvents(context.Context, domain.ID, int64) ([]sharedjob.Event, error) {
	return nil, nil
}

type clockFake struct{}

func (clockFake) Now() time.Time { return time.Unix(1, 0).UTC() }

type explainerFake struct{}

func (explainerFake) Explain(context.Context, ExplanationInput) (ExplanationOutput, error) {
	return ExplanationOutput{}, nil
}

var (
	_ RevisionReader          = upstreamFake{}
	_ DiffReader              = upstreamFake{}
	_ BaselineReader          = upstreamFake{}
	_ ValidationGate          = upstreamFake{}
	_ ProjectionSummaryReader = upstreamFake{}
	_ GraphReadinessReader    = upstreamFake{}
	_ ReportStore             = reportFake{}
	_ JobStore                = jobFake{}
	_ EventStore              = eventFake{}
	_ Clock                   = clockFake{}
	_ EvidenceExplainer       = explainerFake{}
)
