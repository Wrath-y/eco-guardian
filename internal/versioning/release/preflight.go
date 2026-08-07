package release

import (
	"context"
	"errors"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/validation"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var (
	ErrReleaseCapabilityDisabled = errors.New("release capability is disabled")
	ErrPreflightFailed           = errors.New("release preflight failed")
	ErrPreflightUnavailable      = errors.New("release preflight dependency is unavailable")
	ErrValidationManifest        = errors.New("revision cannot supply an exact validation manifest")
	ErrReleaseBaseConflict       = errors.New("release baseline changed")
)

type RecheckStage string

const (
	RecheckWorkerStart  RecheckStage = "worker_start"
	RecheckBeforeBackup RecheckStage = "before_backup"
	RecheckBeforeGraph  RecheckStage = "before_graph_activation"
	RecheckBeforeCommit RecheckStage = "before_pointer_commit"
)

func (s RecheckStage) Valid() bool {
	return s == RecheckWorkerStart || s == RecheckBeforeBackup || s == RecheckBeforeGraph || s == RecheckBeforeCommit
}

// GateResultSource reads results produced by registered capabilities. It
// cannot run a simulation, risk calculation, or backup: preflight consumes
// existing exact evidence only.
type GateResultSource interface {
	Results(context.Context, versioningrevision.CandidateContext, versioningpolicy.ReleasePolicy) ([]versioninggate.Result, error)
}

// PreflightService performs all synchronous checks required before a durable
// release Job may be inserted. Its ports are reads/checks only.
type PreflightService struct {
	Sources    CommandSources
	Validation versioninggate.ValidationSource
	Registry   *versioninggate.Registry
	Results    GateResultSource
}

type Preflight struct {
	Validated  ValidatedCommand
	Validation versioninggate.ResultState
	Assessment versioninggate.Assessment
	Results    []versioninggate.Result
}

// Preflight validates a canonical command, requires an exact #6 FULL pass,
// then assesses all policy Gates against the same candidate/policy/baseline.
// It never creates a Job or asks downstream services to produce new results.
func (s PreflightService) Preflight(ctx context.Context, command Command) (Preflight, error) {
	validated, err := ValidateCommand(ctx, s.Sources, command)
	if err != nil {
		if errors.Is(err, ErrBaselineConflict) {
			return Preflight{}, ErrReleaseBaseConflict
		}
		return Preflight{}, err
	}
	if capability := versioninggate.CalculateReleaseCapability(s.Registry, validated.Policy); !capability.Enabled {
		return Preflight{}, fmt.Errorf("%w: %v", ErrReleaseCapabilityDisabled, capability.Reasons)
	}
	versions, err := validationManifest(validated.Revision.Metadata.Manifest)
	if err != nil {
		return Preflight{}, err
	}
	if s.Validation == nil {
		return Preflight{}, ErrPreflightUnavailable
	}
	validationState, err := (versioninggate.ValidationGateAdapter{Source: s.Validation}).Check(ctx, validated.Candidate.RevisionID, validated.Candidate.ConfigHash, versions)
	if err != nil || validationState == versioninggate.Unavailable {
		return Preflight{}, fmt.Errorf("%w: exact FULL validation", ErrPreflightUnavailable)
	}
	if validationState != versioninggate.Pass {
		return Preflight{}, fmt.Errorf("%w: exact FULL validation is %s", ErrPreflightFailed, validationState)
	}
	if s.Results == nil {
		return Preflight{}, ErrPreflightUnavailable
	}
	results, err := s.Results.Results(ctx, validated.Candidate, validated.Policy)
	if err != nil {
		return Preflight{}, fmt.Errorf("%w: gate results", ErrPreflightUnavailable)
	}
	assessment := versioninggate.AssessPolicy(validated.Candidate, validated.Policy, results)
	if err := ValidateConfirmations(validated.Candidate, validated.Pointer.ReleaseID, assessment, results, command.Confirmations, command.Override); err != nil {
		// A blocking Gate with no requested override is an ordinary preflight
		// failure. A supplied but invalid override remains an explicit
		// confirmation error for the HTTP Problem Details mapper.
		if assessment.State == versioninggate.Block && command.Override == nil && errors.Is(err, ErrOverrideInvalid) {
			return Preflight{}, ErrPreflightFailed
		}
		return Preflight{}, err
	}
	return Preflight{Validated: validated, Validation: validationState, Assessment: assessment, Results: append([]versioninggate.Result(nil), results...)}, nil
}

// Recheck repeats the complete, read-only preflight at every worker boundary
// before an irreversible step. It intentionally does not reuse an earlier
// assessment: a changed active pointer becomes RELEASE_BASE_CONFLICT.
func (s PreflightService) Recheck(ctx context.Context, stage RecheckStage, command Command) (Preflight, error) {
	if !stage.Valid() {
		return Preflight{}, ErrPreflightFailed
	}
	return s.Preflight(ctx, command)
}

// validationManifest reconstructs the narrow #6 manifest from identities
// frozen on the revision. It never falls back to current implementation
// versions when an old revision lacks one of the required entries.
func validationManifest(manifest versioningrevision.VersionManifest) (validation.VersionManifest, error) {
	if !manifest.Valid() {
		return validation.VersionManifest{}, ErrValidationManifest
	}
	values := map[string]string{}
	for _, entry := range manifest.Entries {
		switch entry.CapabilityID {
		case "schema", "dsl", "validator-registry", "numeric-policy":
			if entry.State != versioningrevision.Registered || entry.ContractVersion != "validation-v1" || entry.ImplementationVersion == "" {
				return validation.VersionManifest{}, ErrValidationManifest
			}
			values[entry.CapabilityID] = entry.ImplementationVersion
		}
	}
	versions := validation.VersionManifest{Schema: values["schema"], DSL: values["dsl"], Registry: values["validator-registry"], NumericPolicy: values["numeric-policy"]}
	if !versions.Valid() {
		return validation.VersionManifest{}, ErrValidationManifest
	}
	return versions, nil
}
