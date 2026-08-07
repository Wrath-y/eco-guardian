package app

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioningdiff "github.com/zouyi/eco-guardian/internal/versioning/diff"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var ErrVersioningOperationUnavailable = errors.New("versioning operation is unavailable")

// VersioningService is the application boundary consumed by HTTP adapters.
// It presents immutable versioning operations without exposing a storage
// implementation, a Gate registry, or worker ownership to transport code.
type VersioningService interface {
	ListRevisionRecords(context.Context, string, int) (versioningrevision.HistoryPage, error)
	GetRevisionDetail(context.Context, domain.ID) (versioningrevision.Detail, error)
	CreateCheckpoint(context.Context, domain.ID, string, string) (domain.RevisionSummary, error)
	RestoreRelease(context.Context, domain.ID, domain.ID) (domain.RevisionSummary, error)
	CompareRevisions(context.Context, domain.ID, domain.ID) ([]versioningdiff.FieldChange, error)

	ListPolicies(context.Context, string, int) (versioningpolicy.Page, error)
	GetPolicy(context.Context, domain.ID) (versioningpolicy.ReleasePolicy, error)
	CreatePolicy(context.Context, versioningpolicy.Definition) (versioningpolicy.ReleasePolicy, error)

	ListReleaseRecords(context.Context, string, int) (versioningrelease.ReadPage, error)
	GetReleaseRecord(context.Context, domain.ID) (ReleaseDetail, error)
	CreateRelease(context.Context, versioningrelease.Command) (versioningrelease.Job, error)

	GetReleaseJob(context.Context, domain.ID) (versioningrelease.Job, error)
	ListReleaseJobEvents(context.Context, domain.ID, int64) ([]versioningrelease.Event, error)
	CancelReleaseJob(context.Context, domain.ID) (versioningrelease.Job, bool, error)
	ReleaseCapability(context.Context) (versioninggate.ReleaseCapability, error)
}

// ReleaseDetail combines immutable release audit data with the singleton
// pointer snapshot observed during the same request. It does not imply that a
// historical release has mutable status.
type ReleaseDetail struct {
	Record  versioningrelease.ReadRecord
	Pointer versioningrelease.ActivePointer
}

type revisionOperations interface {
	ListRevisionRecords(context.Context, string, int) (versioningrevision.HistoryPage, error)
	GetRevisionDetail(context.Context, domain.ID) (versioningrevision.Detail, error)
	CreateCheckpoint(context.Context, domain.ID, string, string) (domain.RevisionSummary, error)
	RestoreRelease(context.Context, domain.ID, domain.ID) (domain.RevisionSummary, error)
}
type diffReader interface {
	Materialize(context.Context, domain.ID) ([]versioningdiff.EntityBlob, error)
}
type policyOperations interface {
	ListPolicies(context.Context, string, int) (versioningpolicy.Page, error)
	GetPolicy(context.Context, domain.ID) (versioningpolicy.ReleasePolicy, error)
	CreatePolicy(context.Context, versioningpolicy.Definition, versioningpolicy.ContractCatalog) (versioningpolicy.ReleasePolicy, error)
}
type releaseOperations interface {
	ListReleases(context.Context, string, int) (versioningrelease.ReadPage, error)
	GetReleaseRecord(context.Context, domain.ID) (versioningrelease.ReadRecord, error)
	GetActivePointer(context.Context) (versioningrelease.ActivePointer, error)
}
type jobReader interface {
	GetReleaseJob(context.Context, domain.ID) (versioningrelease.Job, error)
	ListReleaseJobEvents(context.Context, domain.ID, int64) ([]versioningrelease.Event, error)
}

// VersioningApplication is composition glue. Each dependency is a narrow
// domain port, so tests may use fakes and transports cannot accidentally gain
// direct SQL access.
type VersioningApplication struct {
	Revisions revisionOperations
	Diff      diffReader
	Policies  policyOperations
	Releases  releaseOperations
	Jobs      jobReader
	Catalog   versioningpolicy.ContractCatalog
	Registry  *versioninggate.Registry
	Submit    func(context.Context, versioningrelease.Command) (versioningrelease.Job, error)
	Cancel    func(context.Context, domain.ID) (versioningrelease.Job, bool, error)
}

// VersioningDependencies are process-owned dependencies supplied at
// composition time. They remain optional while later capability changes are
// not installed; missing dependencies make only the affected release action
// unavailable, never revision history or diff.
type VersioningDependencies struct {
	Catalog  versioningpolicy.ContractCatalog
	Registry *versioninggate.Registry
	Submit   func(context.Context, versioningrelease.Command) (versioningrelease.Job, error)
	Cancel   func(context.Context, domain.ID) (versioningrelease.Job, bool, error)
}

func (s VersioningApplication) ListRevisionRecords(ctx context.Context, cursor string, limit int) (versioningrevision.HistoryPage, error) {
	if s.Revisions == nil {
		return versioningrevision.HistoryPage{}, ErrVersioningOperationUnavailable
	}
	return s.Revisions.ListRevisionRecords(ctx, cursor, limit)
}
func (s VersioningApplication) GetRevisionDetail(ctx context.Context, id domain.ID) (versioningrevision.Detail, error) {
	if s.Revisions == nil {
		return versioningrevision.Detail{}, ErrVersioningOperationUnavailable
	}
	return s.Revisions.GetRevisionDetail(ctx, id)
}
func (s VersioningApplication) CreateCheckpoint(ctx context.Context, id domain.ID, name, description string) (domain.RevisionSummary, error) {
	if s.Revisions == nil {
		return domain.RevisionSummary{}, ErrVersioningOperationUnavailable
	}
	return s.Revisions.CreateCheckpoint(ctx, id, name, description)
}
func (s VersioningApplication) RestoreRelease(ctx context.Context, current, source domain.ID) (domain.RevisionSummary, error) {
	if s.Revisions == nil {
		return domain.RevisionSummary{}, ErrVersioningOperationUnavailable
	}
	return s.Revisions.RestoreRelease(ctx, current, source)
}
func (s VersioningApplication) CompareRevisions(ctx context.Context, base, target domain.ID) ([]versioningdiff.FieldChange, error) {
	if s.Diff == nil {
		return nil, ErrVersioningOperationUnavailable
	}
	return versioningdiff.CompareRevisions(ctx, s.Diff, base, target)
}
func (s VersioningApplication) ListPolicies(ctx context.Context, cursor string, limit int) (versioningpolicy.Page, error) {
	if s.Policies == nil {
		return versioningpolicy.Page{}, ErrVersioningOperationUnavailable
	}
	return s.Policies.ListPolicies(ctx, cursor, limit)
}
func (s VersioningApplication) GetPolicy(ctx context.Context, id domain.ID) (versioningpolicy.ReleasePolicy, error) {
	if s.Policies == nil {
		return versioningpolicy.ReleasePolicy{}, ErrVersioningOperationUnavailable
	}
	return s.Policies.GetPolicy(ctx, id)
}
func (s VersioningApplication) CreatePolicy(ctx context.Context, definition versioningpolicy.Definition) (versioningpolicy.ReleasePolicy, error) {
	if s.Policies == nil || s.Catalog == nil {
		return versioningpolicy.ReleasePolicy{}, ErrVersioningOperationUnavailable
	}
	return s.Policies.CreatePolicy(ctx, definition, s.Catalog)
}
func (s VersioningApplication) ListReleaseRecords(ctx context.Context, cursor string, limit int) (versioningrelease.ReadPage, error) {
	if s.Releases == nil {
		return versioningrelease.ReadPage{}, ErrVersioningOperationUnavailable
	}
	return s.Releases.ListReleases(ctx, cursor, limit)
}
func (s VersioningApplication) GetReleaseRecord(ctx context.Context, id domain.ID) (ReleaseDetail, error) {
	if s.Releases == nil {
		return ReleaseDetail{}, ErrVersioningOperationUnavailable
	}
	record, err := s.Releases.GetReleaseRecord(ctx, id)
	if err != nil {
		return ReleaseDetail{}, err
	}
	pointer, err := s.Releases.GetActivePointer(ctx)
	if err != nil {
		return ReleaseDetail{}, err
	}
	return ReleaseDetail{Record: record, Pointer: pointer}, nil
}
func (s VersioningApplication) CreateRelease(ctx context.Context, command versioningrelease.Command) (versioningrelease.Job, error) {
	if s.Submit == nil {
		return versioningrelease.Job{}, ErrVersioningOperationUnavailable
	}
	return s.Submit(ctx, command)
}
func (s VersioningApplication) GetReleaseJob(ctx context.Context, id domain.ID) (versioningrelease.Job, error) {
	if s.Jobs == nil {
		return versioningrelease.Job{}, ErrVersioningOperationUnavailable
	}
	return s.Jobs.GetReleaseJob(ctx, id)
}
func (s VersioningApplication) ListReleaseJobEvents(ctx context.Context, id domain.ID, after int64) ([]versioningrelease.Event, error) {
	if s.Jobs == nil {
		return nil, ErrVersioningOperationUnavailable
	}
	return s.Jobs.ListReleaseJobEvents(ctx, id, after)
}
func (s VersioningApplication) CancelReleaseJob(ctx context.Context, id domain.ID) (versioningrelease.Job, bool, error) {
	if s.Cancel == nil {
		return versioningrelease.Job{}, false, ErrVersioningOperationUnavailable
	}
	return s.Cancel(ctx, id)
}
func (s VersioningApplication) ReleaseCapability(ctx context.Context) (versioninggate.ReleaseCapability, error) {
	if s.Policies == nil {
		return versioninggate.ReleaseCapability{}, ErrVersioningOperationUnavailable
	}
	page, err := s.Policies.ListPolicies(ctx, "", 1)
	if err != nil {
		return versioninggate.ReleaseCapability{}, err
	}
	if len(page.Items) == 0 {
		return versioninggate.ReleaseCapability{Reasons: []versioninggate.DisabledReason{{Reason: "no release policy is available"}}}, nil
	}
	return versioninggate.CalculateReleaseCapability(s.Registry, page.Items[0]), nil
}

var _ VersioningService = VersioningApplication{}
