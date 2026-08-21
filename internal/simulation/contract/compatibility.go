package contract

import "fmt"

type HistoricalRunProjection struct {
	CanonicalResult, InputHash, FingerprintHash, ResultHash string
	Reproducible                                            bool
	Reasons                                                 []string
}

// ImplementationRegistry projects the retained implementation set used for
// historical runs. A stored descriptor must remain byte-identical to replay.
type ImplementationRegistry struct{ current *ManifestRegistry }

func NewImplementationRegistry(current *ManifestRegistry) (*ImplementationRegistry, error) {
	if current == nil || len(current.Descriptors()) != len(RequiredV1Descriptors) {
		return nil, fmt.Errorf("incomplete simulation implementation registry")
	}
	return &ImplementationRegistry{current: current}, nil
}

// ProjectHistoricalRun preserves stored facts even when their implementation
// can no longer be replayed; callers must not offer verify/Gate in that case.
func (r *ImplementationRegistry) ProjectHistoricalRun(canonicalResult, inputHash, fingerprintHash, resultHash string, recorded []Descriptor) HistoricalRunProjection {
	projection := HistoricalRunProjection{CanonicalResult: canonicalResult, InputHash: inputHash, FingerprintHash: fingerprintHash, ResultHash: resultHash, Reproducible: true}
	if err := r.AuditHistorical(recorded); err != nil {
		projection.Reproducible = false
		projection.Reasons = []string{err.Error()}
	}
	return projection
}

func (r *ImplementationRegistry) AuditHistorical(recorded []Descriptor) error {
	if r == nil || len(recorded) == 0 {
		return fmt.Errorf("simulation compatibility history is required")
	}
	for _, old := range recorded {
		current, found := r.current.Descriptor(old.ID)
		if !found || current.Version != old.Version || current.Hash != old.Hash {
			return fmt.Errorf("simulation implementation unavailable: %s@%s", old.ID, old.Version)
		}
	}
	return nil
}
