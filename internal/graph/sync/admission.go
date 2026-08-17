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
