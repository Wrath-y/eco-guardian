package sync

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

var ErrValidationNotPassed = errors.New("graph admission requires exact full validation pass")

// FullValidationGate is the narrow #6 seam. Admission never revalidates or
// weakens the revision/hash/version identity it receives.
type FullValidationGate interface {
	Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error)
}

// JobAdmission is implemented by the existing durable Job repository adapter.
// Graph orchestration owns no parallel Job table or idempotency mechanism.
type JobAdmission interface {
	CreateOrGetGraphJob(context.Context, GraphJobRequest) (GraphJob, bool, error)
}

type GraphJobRequest struct {
	ProjectID, RevisionID                  domain.ID
	InputHash, IdempotencyKey, RequestHash string
}
type GraphJob struct {
	ID                    domain.ID
	ProjectID, RevisionID domain.ID
	InputHash             string
}

func (r GraphJobRequest) Valid() bool {
	return r.ProjectID.Valid() && r.RevisionID.Valid() && len(r.InputHash) == 64 && r.IdempotencyKey != "" && len(r.RequestHash) == 64
}
func (j GraphJob) Valid() bool {
	return j.ID.Valid() && j.ProjectID.Valid() && j.RevisionID.Valid() && len(j.InputHash) == 64
}

type AdmissionService struct {
	Validation FullValidationGate
	Jobs       JobAdmission
}

func (s AdmissionService) Admit(ctx context.Context, request GraphJobRequest, versions validation.VersionManifest) (GraphJob, bool, error) {
	if !request.Valid() || s.Jobs == nil {
		return GraphJob{}, false, ErrValidationNotPassed
	}
	if err := RequireFullValidation(ctx, s.Validation, request.RevisionID, request.InputHash, versions); err != nil {
		return GraphJob{}, false, err
	}
	return s.Jobs.CreateOrGetGraphJob(ctx, request)
}

func RequireFullValidation(ctx context.Context, gate FullValidationGate, revisionID domain.ID, configHash string, versions validation.VersionManifest) error {
	if gate == nil || !revisionID.Valid() || configHash == "" || !versions.Valid() {
		return ErrValidationNotPassed
	}
	result, err := gate.Check(ctx, revisionID, configHash, versions)
	if err != nil || result != validation.GatePass {
		return ErrValidationNotPassed
	}
	return nil
}
