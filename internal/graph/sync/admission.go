package sync

import (
	"context"
	"errors"
	"strings"

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
	Evidence                               string
}
type GraphJob struct {
	ID                    domain.ID
	ProjectID, RevisionID domain.ID
	InputHash             string
	IdempotencyKey        string
	RequestHash           string
	Evidence              string
	Status                JobStatus
	Result                *GraphJobResult
}

func (r GraphJobRequest) Valid() bool {
	return r.ProjectID.Valid() && r.RevisionID.Valid() && validHash(r.InputHash) && validIdempotencyKey(r.IdempotencyKey) && validHash(r.RequestHash) && len(r.Evidence) <= 4096
}
func (j GraphJob) Valid() bool {
	return j.ID.Valid() && j.ProjectID.Valid() && j.RevisionID.Valid() && validHash(j.InputHash) && validIdempotencyKey(j.IdempotencyKey) && validHash(j.RequestHash) && len(j.Evidence) <= 4096 && j.Status.Valid() && (j.Result == nil || j.Result.Valid()) && (j.Status != JobSucceeded || j.Result != nil)
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	return strings.Trim(value, "0123456789abcdef") == ""
}

func validIdempotencyKey(key string) bool {
	return key != "" && key == strings.TrimSpace(key) && len(key) <= 256
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
