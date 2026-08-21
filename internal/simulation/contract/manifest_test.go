package contract

import "testing"

func TestV1ManifestIsClosedAndStable(t *testing.T) {
	registry, err := NewManifestRegistry(RequiredV1Descriptors, V1Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(registry.Descriptors()); got != len(RequiredV1Descriptors) {
		t.Fatalf("registered descriptor count=%d", got)
	}
	first, found := registry.Descriptor("simulation-prng")
	if !found || first.Version != "v1" || len(first.Hash) != 64 {
		t.Fatalf("descriptor=%#v found=%v", first, found)
	}
	first.Dependencies = append(first.Dependencies, "mutated")
	second, _ := registry.Descriptor("simulation-prng")
	if len(second.Dependencies) != 0 {
		t.Fatalf("registry leaked mutable descriptor: %#v", second)
	}
}

func TestManifestRejectsMissingDependenciesAndSameVersionDrift(t *testing.T) {
	missing := StableDescriptor("engine", "v1", "event")
	if _, err := NewManifestRegistry([]string{"engine"}, []Descriptor{missing}); err == nil {
		t.Fatal("expected missing dependency error")
	}
	first := StableDescriptor("engine", "v1")
	drifted := first
	drifted.Hash = StableDescriptor("engine", "v1", "different-semantics").Hash
	if _, err := NewManifestRegistry([]string{"engine"}, []Descriptor{first, drifted}); err == nil {
		t.Fatal("expected same-version drift error")
	}
}
