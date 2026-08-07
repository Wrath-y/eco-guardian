package gate

import (
	"context"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

// ValidationSource is the narrow #6 dependency. It preserves the existing
// exact FULL-validation semantics rather than copying or weakening them.
type ValidationSource interface {
	Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error)
}

// ValidationGateAdapter exposes the existing #6 exact-FULL gate as a
// Registry-compatible capability without reproducing validation behavior.
type ValidationGateAdapter struct{ Source ValidationSource }

const (
	ValidationCapabilityID   = "validation"
	ValidationGateID         = "full"
	ValidationContract       = "validation-v1"
	ValidationImplementation = "validation-gate-v1"
)

func (a ValidationGateAdapter) Descriptor() Descriptor {
	return Descriptor{
		CapabilityID: ValidationCapabilityID, GateID: ValidationGateID,
		ContractVersion: ValidationContract, ImplementationVersion: ValidationImplementation,
		RequiredInputs:  []string{"revision_id", "config_hash", "schema_version", "dsl_version", "registry_version", "numeric_policy_version"},
		SupportedStates: []ResultState{Pass, Block, Unavailable, Stale},
		// Deterministic validation blocks, including cycles and unbounded
		// constructs, are never numeric-risk overrides.
		OverridableNumericBlock: false,
	}
}

func (a ValidationGateAdapter) Check(ctx context.Context, revisionID domain.ID, configHash string, versions validation.VersionManifest) (ResultState, error) {
	result, err := a.Source.Check(ctx, revisionID, configHash, versions)
	if err != nil {
		return Unavailable, err
	}
	switch result {
	case validation.GatePass:
		return Pass, nil
	case validation.GateBlocked:
		return Block, nil
	case validation.GateRequiresValidation:
		return Stale, nil
	default:
		return Unavailable, nil
	}
}

// EvidenceReader is implemented by installed capabilities. Versioning stores
// only identities and evidence; it does not implement Graph, simulation, risk,
// or backup business logic.
type EvidenceReader interface {
	ReadEvidence(context.Context, string) ([]byte, error)
}

// Provider is the registration-only boundary shared by later capabilities.
// It intentionally has no projection, simulation, risk, threshold, or backup
// methods: those business operations remain owned by their respective change.
type Provider interface{ Descriptor() Descriptor }

type GraphProvider interface{ Provider }
type SimulationProvider interface{ Provider }
type RiskProvider interface{ Provider }
type BackupProvider interface{ Provider }
