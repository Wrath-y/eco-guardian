package release

import (
	"context"
	"errors"
	"fmt"

	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var (
	ErrCommandInvalid    = errors.New("release command is invalid")
	ErrCommandSources    = errors.New("release command sources are unavailable")
	ErrCandidateUnknown  = errors.New("release candidate revision is unknown")
	ErrCandidateMismatch = errors.New("release candidate does not match immutable revision")
	ErrPolicyUnknown     = errors.New("release policy is unknown")
	ErrPolicyInvalid     = errors.New("release policy is invalid")
	ErrBaselineUnknown   = errors.New("release baseline is unknown")
	ErrBaselineConflict  = errors.New("release baseline does not match current active release")
)

// ValidatedCommand is the immutable snapshot used by synchronous preflight.
// It contains no Job state, which makes validation safe to call before any
// durable side effect is created.
type ValidatedCommand struct {
	Command     Command
	Candidate   versioningrevision.CandidateContext
	Revision    versioningrevision.Record
	Policy      versioningpolicy.ReleasePolicy
	Pointer     ActivePointer
	RequestHash string
}

// ValidateCommand checks the complete request identity against immutable
// storage and the current pointer. Gate evaluation intentionally happens in
// the following preflight stage; this function performs no writes.
func ValidateCommand(ctx context.Context, sources CommandSources, command Command) (ValidatedCommand, error) {
	if !command.Valid() {
		return ValidatedCommand{}, ErrCommandInvalid
	}
	if sources == nil {
		return ValidatedCommand{}, ErrCommandSources
	}
	revision, err := sources.GetRevisionRecord(ctx, command.CandidateRevisionID)
	if err != nil {
		return ValidatedCommand{}, fmt.Errorf("%w: %v", ErrCandidateUnknown, err)
	}
	if !revision.Valid() || revision.Metadata.RevisionID != command.CandidateRevisionID || revision.Metadata.ConfigHash != command.ConfigHash || revision.Metadata.ManifestHash != command.ManifestHash {
		return ValidatedCommand{}, ErrCandidateMismatch
	}
	policy, err := sources.GetPolicy(ctx, command.PolicyID)
	if err != nil {
		return ValidatedCommand{}, fmt.Errorf("%w: %v", ErrPolicyUnknown, err)
	}
	if !policy.Valid() || policy.ID != command.PolicyID {
		return ValidatedCommand{}, ErrPolicyInvalid
	}
	pointer, err := sources.GetActivePointer(ctx)
	if err != nil || !pointer.Valid() {
		return ValidatedCommand{}, fmt.Errorf("%w: active pointer unavailable", ErrCommandSources)
	}
	if command.BaselineReleaseID != "" {
		baseline, getErr := sources.GetRelease(ctx, command.BaselineReleaseID)
		if getErr != nil || baseline.ID != command.BaselineReleaseID {
			return ValidatedCommand{}, ErrBaselineUnknown
		}
	}
	if command.BaselineReleaseID != pointer.ReleaseID {
		return ValidatedCommand{}, ErrBaselineConflict
	}
	candidate := versioningrevision.CandidateContext{
		RevisionID:        command.CandidateRevisionID,
		ConfigHash:        command.ConfigHash,
		ManifestHash:      command.ManifestHash,
		PolicyID:          command.PolicyID,
		BaselineReleaseID: command.BaselineReleaseID,
	}
	// Baseline confirmation is a request-level rule. Warning and numeric-risk
	// confirmations need Gate facts and are validated by the synchronous
	// preflight evaluator.
	if err := ValidateConfirmations(candidate, pointer.ReleaseID, versioninggate.Assessment{State: versioninggate.Pass}, nil, command.Confirmations, nil); err != nil {
		return ValidatedCommand{}, err
	}
	requestHash, err := command.Hash()
	if err != nil {
		return ValidatedCommand{}, fmt.Errorf("%w: canonical request: %v", ErrCommandInvalid, err)
	}
	return ValidatedCommand{Command: command, Candidate: candidate, Revision: revision, Policy: policy, Pointer: pointer, RequestHash: requestHash}, nil
}
