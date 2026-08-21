package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Descriptor freezes one independently versioned simulation interpretation.
// Hash is an explicit compatibility identity rather than a build timestamp.
type Descriptor struct {
	ID           string
	Version      string
	Hash         string
	Dependencies []string
}

func (d Descriptor) Valid() bool {
	return strings.TrimSpace(d.ID) != "" && strings.TrimSpace(d.Version) != "" && len(d.Hash) == 64 && validDependencies(d.Dependencies)
}

func validDependencies(dependencies []string) bool {
	seen := make(map[string]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		if strings.TrimSpace(dependency) == "" {
			return false
		}
		if _, duplicate := seen[dependency]; duplicate {
			return false
		}
		seen[dependency] = struct{}{}
	}
	return true
}

// StableDescriptor makes source-controlled compatibility hashes deterministic.
// A semantic change must select a new version or update the frozen hash.
func StableDescriptor(id, version string, dependencies ...string) Descriptor {
	deps := append([]string(nil), dependencies...)
	sort.Strings(deps)
	digest := sha256.Sum256([]byte("simulation-descriptor-v1\x00" + id + "\x00" + version + "\x00" + strings.Join(deps, "\x00")))
	return Descriptor{ID: id, Version: version, Hash: hex.EncodeToString(digest[:]), Dependencies: deps}
}

// ManifestRegistry validates the closed v1 component set at startup. It is
// independent of storage and optional capability packages.
type ManifestRegistry struct{ descriptors map[string]Descriptor }

func NewManifestRegistry(required []string, descriptors []Descriptor) (*ManifestRegistry, error) {
	if len(descriptors) == 0 {
		return nil, fmt.Errorf("simulation descriptors are required")
	}
	registered := make(map[string]Descriptor, len(descriptors))
	for _, descriptor := range descriptors {
		if !descriptor.Valid() {
			return nil, fmt.Errorf("invalid simulation descriptor %q", descriptor.ID)
		}
		if existing, duplicate := registered[descriptor.ID]; duplicate {
			if existing.Version == descriptor.Version && existing.Hash != descriptor.Hash {
				return nil, fmt.Errorf("same-version semantic drift for simulation descriptor %q", descriptor.ID)
			}
			return nil, fmt.Errorf("duplicate simulation descriptor %q", descriptor.ID)
		}
		registered[descriptor.ID] = copyDescriptor(descriptor)
	}
	for _, descriptor := range registered {
		for _, dependency := range descriptor.Dependencies {
			if _, found := registered[dependency]; !found {
				return nil, fmt.Errorf("simulation descriptor %q has missing dependency %q", descriptor.ID, dependency)
			}
		}
	}
	for _, id := range required {
		if _, found := registered[id]; !found {
			return nil, fmt.Errorf("missing required simulation descriptor %q", id)
		}
	}
	return &ManifestRegistry{descriptors: registered}, nil
}

func (r *ManifestRegistry) Descriptor(id string) (Descriptor, bool) {
	descriptor, found := r.descriptors[id]
	return copyDescriptor(descriptor), found
}

func (r *ManifestRegistry) Descriptors() []Descriptor {
	items := make([]Descriptor, 0, len(r.descriptors))
	for _, descriptor := range r.descriptors {
		items = append(items, copyDescriptor(descriptor))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func copyDescriptor(descriptor Descriptor) Descriptor {
	descriptor.Dependencies = append([]string(nil), descriptor.Dependencies...)
	return descriptor
}

var RequiredV1Descriptors = []string{
	"simulation-input-schema", "simulation-result-schema", "simulation-scene-contract",
	"simulation-engine", "simulation-event", "simulation-time", "simulation-prng",
	"simulation-evaluator-adapter", "metric-dps", "metric-healing", "metric-survivability",
	"metric-resource", "metric-control", "simulation-aggregation",
}

func V1Descriptors() []Descriptor {
	return []Descriptor{
		StableDescriptor("simulation-input-schema", "v1"),
		StableDescriptor("simulation-result-schema", "v1"),
		StableDescriptor("simulation-scene-contract", "v1", "simulation-input-schema"),
		StableDescriptor("simulation-engine", "v1", "simulation-event", "simulation-time", "simulation-prng", "simulation-evaluator-adapter"),
		StableDescriptor("simulation-event", "v1"),
		StableDescriptor("simulation-time", "v1"),
		StableDescriptor("simulation-prng", "v1"),
		StableDescriptor("simulation-evaluator-adapter", "v1"),
		StableDescriptor("metric-dps", "v1", "simulation-event"),
		StableDescriptor("metric-healing", "v1", "simulation-event"),
		StableDescriptor("metric-survivability", "v1", "simulation-event"),
		StableDescriptor("metric-resource", "v1", "simulation-event"),
		StableDescriptor("metric-control", "v1", "simulation-event"),
		StableDescriptor("simulation-aggregation", "v1", "simulation-result-schema"),
	}
}
