package gate

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/validation"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var ErrFullValidationUnavailable = errors.New("simulation full validation is unavailable")

type revisionReader interface {
	GetRevisionRecord(context.Context, domain.ID) (versioningrevision.Record, error)
}
type fullChecker interface {
	Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error)
}

// FullValidationAdapter is the sole bridge from the simulation port to #6.
// It reads only the requested immutable revision and never falls back to a
// current manifest or a LOCAL validation run.
type FullValidationAdapter struct {
	Revisions revisionReader
	Gate      fullChecker
}

func (a FullValidationAdapter) RequireFull(ctx context.Context, id contract.ID) error {
	if a.Revisions == nil || a.Gate == nil || !domain.ID(id).Valid() {
		return ErrFullValidationUnavailable
	}
	record, err := a.Revisions.GetRevisionRecord(ctx, domain.ID(id))
	if err != nil || !record.Valid() || record.Metadata.RevisionID != domain.ID(id) {
		return ErrFullValidationUnavailable
	}
	versions, ok := validationVersions(record.Metadata.Manifest)
	if !ok {
		return ErrFullValidationUnavailable
	}
	result, err := a.Gate.Check(ctx, domain.ID(id), record.Metadata.ConfigHash, versions)
	if err != nil || result != validation.GatePass {
		return ErrFullValidationUnavailable
	}
	return nil
}

func validationVersions(manifest versioningrevision.VersionManifest) (validation.VersionManifest, bool) {
	values := map[string]string{}
	for _, entry := range manifest.Entries {
		if entry.State == versioningrevision.Registered {
			values[entry.CapabilityID] = entry.ImplementationVersion
		}
	}
	versions := validation.VersionManifest{Schema: values["schema"], DSL: values["dsl"], Registry: values["validator-registry"], NumericPolicy: values["numeric-policy"]}
	return versions, versions.Valid()
}

var _ contract.ValidationGate = FullValidationAdapter{}
