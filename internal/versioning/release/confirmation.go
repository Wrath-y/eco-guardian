package release

import (
	"errors"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

const (
	ConfirmationEstablishBaseline  = "establish_baseline"
	ConfirmationAcknowledgeWarning = "acknowledge_warning"
	ConfirmationNumericOverride    = "numeric_override"
)

var (
	ErrBaselineConfirmation = errors.New("baseline confirmation is invalid")
	ErrWarningConfirmation  = errors.New("warning confirmation is required")
	ErrOverrideInvalid      = errors.New("numeric risk override is invalid")
)

func hasConfirmation(confirmations []Confirmation, kind string) bool {
	for _, confirmation := range confirmations {
		if confirmation.Kind == kind && confirmation.Confirmed {
			return true
		}
	}
	return false
}

// ValidateConfirmations binds release confirmations to the server's active
// pointer and immutable Gate facts. Only a Gate-declared numeric-risk block
// may be overridden; deterministic/policy/capability/backup blocks never are.
func ValidateConfirmations(candidate versioningrevision.CandidateContext, activeReleaseID domain.ID, assessment versioninggate.Assessment, results []versioninggate.Result, confirmations []Confirmation, override *OverrideAudit) error {
	if activeReleaseID == "" {
		if candidate.BaselineReleaseID != "" || !hasConfirmation(confirmations, ConfirmationEstablishBaseline) {
			return ErrBaselineConfirmation
		}
	} else {
		if candidate.BaselineReleaseID != activeReleaseID {
			return ErrBaselineConfirmation
		}
		for _, result := range results {
			if result.Descriptor.CapabilityID == "risk" && result.Context.Candidate.BaselineReleaseID != activeReleaseID {
				return ErrBaselineConfirmation
			}
		}
	}
	if assessment.State == versioninggate.Warning && !hasConfirmation(confirmations, ConfirmationAcknowledgeWarning) {
		return ErrWarningConfirmation
	}
	if assessment.State != versioninggate.Block {
		return nil
	}
	if override == nil || !override.GateResultID.Valid() || !override.Confirmed || strings.TrimSpace(override.Reason) == "" {
		return ErrOverrideInvalid
	}
	for _, result := range results {
		if result.ID == override.GateResultID && result.State == versioninggate.Block && result.Descriptor.CapabilityID == "risk" && result.Descriptor.OverridableNumericBlock {
			return nil
		}
	}
	return ErrOverrideInvalid
}
