package revision

import (
	"fmt"
	"sort"
)

var requiredCapabilities = []string{"schema", "dsl", "validator-registry", "numeric-policy"}

// Registry freezes the startup contributor set. It copies contributor values
// into a sorted manifest, so later adapter mutation cannot rewrite history.
type Registry struct{ manifest VersionManifest }

func NewRegistry(expectedContracts map[string]string, contributors []VersionContributor) (*Registry, error) {
	if len(contributors) == 0 {
		return nil, fmt.Errorf("version contributors are required")
	}
	entries := make([]VersionEntry, 0, len(contributors))
	seen := make(map[string]struct{}, len(contributors))
	for _, contributor := range contributors {
		if contributor == nil {
			return nil, fmt.Errorf("nil version contributor")
		}
		entry := VersionEntry{
			CapabilityID: contributor.CapabilityID(), ContractVersion: contributor.ContractVersion(),
			ImplementationVersion: contributor.ImplementationVersion(), State: contributor.RegistrationState(),
		}
		if !entry.Valid() {
			return nil, fmt.Errorf("invalid version contributor %q", entry.CapabilityID)
		}
		if _, duplicate := seen[entry.CapabilityID]; duplicate {
			return nil, fmt.Errorf("duplicate version contributor %q", entry.CapabilityID)
		}
		if required, constrained := expectedContracts[entry.CapabilityID]; constrained && required != entry.ContractVersion {
			return nil, fmt.Errorf("incompatible version contributor %q contract %q", entry.CapabilityID, entry.ContractVersion)
		}
		seen[entry.CapabilityID] = struct{}{}
		entries = append(entries, entry)
	}
	for _, capability := range requiredCapabilities {
		if _, ok := seen[capability]; !ok {
			return nil, fmt.Errorf("missing required version contributor %q", capability)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].CapabilityID < entries[j].CapabilityID })
	manifest := VersionManifest{Entries: entries}
	if !manifest.Valid() {
		return nil, fmt.Errorf("invalid version manifest")
	}
	return &Registry{manifest: manifest}, nil
}

func (r *Registry) Manifest() VersionManifest {
	copy := r.manifest
	copy.Entries = append([]VersionEntry(nil), r.manifest.Entries...)
	return copy
}
