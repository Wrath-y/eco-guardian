package app

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

var ErrSimulationFingerprintUnavailable = errors.New("simulation fingerprint is unavailable")

type simulationRevisionRecordReader interface {
	GetRevisionRecord(context.Context, domain.ID) (versioningrevision.Record, error)
}

// SimulationFingerprintResolver combines only the captured input with that
// input's immutable revision manifest and the registered simulation manifest.
type SimulationFingerprintResolver struct {
	Revisions simulationRevisionRecordReader
	Registry  *contract.ManifestRegistry
}

func (r SimulationFingerprintResolver) ResolveSimulationFingerprint(ctx context.Context, input contract.SimulationInputV1) (string, error) {
	if r.Revisions == nil || r.Registry == nil || !domain.ID(input.RevisionID).Valid() {
		return "", ErrSimulationFingerprintUnavailable
	}
	record, err := r.Revisions.GetRevisionRecord(ctx, domain.ID(input.RevisionID))
	if err != nil || !record.Valid() || record.Metadata.RevisionID != domain.ID(input.RevisionID) || record.Metadata.ConfigHash != input.ConfigHash || record.Metadata.ManifestHash != input.ManifestHash {
		return "", ErrSimulationFingerprintUnavailable
	}
	implementations := make([]contract.RevisionImplementation, 0, len(record.Metadata.Manifest.Entries))
	for _, entry := range record.Metadata.Manifest.Entries {
		implementations = append(implementations, contract.RevisionImplementation{CapabilityID: entry.CapabilityID, ContractVersion: entry.ContractVersion, ImplementationVersion: entry.ImplementationVersion, State: string(entry.State)})
	}
	_, fingerprint, err := contract.ResolveFingerprint(r.Registry, input, implementations)
	if err != nil {
		return "", ErrSimulationFingerprintUnavailable
	}
	return fingerprint, nil
}

var _ simulationFingerprintResolver = SimulationFingerprintResolver{}
