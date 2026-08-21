package contract

import "fmt"

// ImplementationRegistry projects the retained implementation set used for
// historical runs. A stored descriptor must remain byte-identical to replay.
type ImplementationRegistry struct{ current *ManifestRegistry }

func NewImplementationRegistry(current *ManifestRegistry) (*ImplementationRegistry, error) {
	if current == nil || len(current.Descriptors()) != len(RequiredV1Descriptors) {
		return nil, fmt.Errorf("incomplete simulation implementation registry")
	}
	return &ImplementationRegistry{current: current}, nil
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
