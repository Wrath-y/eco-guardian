package gate

import (
	"sync"

	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
)

// Registry is the immutable startup registry for installed Gate contracts.
// It deliberately stores descriptors only; capability evaluation and evidence
// retrieval remain behind their respective ports.
type Registry struct {
	mu          sync.RWMutex
	descriptors map[string]Descriptor
}

func NewRegistry(descriptors ...Descriptor) (*Registry, error) {
	registry := &Registry{descriptors: make(map[string]Descriptor, len(descriptors))}
	for _, descriptor := range descriptors {
		if err := registry.Register(descriptor); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func descriptorKey(capabilityID, gateID string) string { return capabilityID + "\x00" + gateID }

func cloneDescriptor(descriptor Descriptor) Descriptor {
	copy := descriptor
	copy.RequiredInputs = append([]string(nil), descriptor.RequiredInputs...)
	copy.SupportedStates = append([]ResultState(nil), descriptor.SupportedStates...)
	return copy
}

func (r *Registry) Register(descriptor Descriptor) error {
	if !descriptor.Valid() {
		return ErrDescriptorInvalid
	}
	key := descriptorKey(descriptor.CapabilityID, descriptor.GateID)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.descriptors[key]; exists {
		return ErrDescriptorDuplicate
	}
	r.descriptors[key] = cloneDescriptor(descriptor)
	return nil
}

func (r *Registry) RegisterProvider(provider Provider) error {
	if provider == nil {
		return ErrDescriptorInvalid
	}
	return r.Register(provider.Descriptor())
}

func (r *Registry) Descriptor(capabilityID, gateID string) (Descriptor, error) {
	r.mu.RLock()
	descriptor, exists := r.descriptors[descriptorKey(capabilityID, gateID)]
	r.mu.RUnlock()
	if !exists {
		return Descriptor{}, ErrDescriptorNotFound
	}
	return cloneDescriptor(descriptor), nil
}

// SupportsCapabilityContract implements policy.ContractCatalog with exact
// capability, gate, and contract-version matching.
func (r *Registry) SupportsCapabilityContract(requirement versioningpolicy.CapabilityRequirement) bool {
	descriptor, err := r.Descriptor(requirement.CapabilityID, requirement.GateID)
	return err == nil && descriptor.ContractVersion == requirement.ContractVersion &&
		(requirement.ImplementationVersion == "" || descriptor.ImplementationVersion == requirement.ImplementationVersion)
}
