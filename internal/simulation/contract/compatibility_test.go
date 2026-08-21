package contract

import "testing"

func TestImplementationRegistryAuditsRetainedDescriptors(t *testing.T) {
	manifest, err := NewManifestRegistry(RequiredV1Descriptors, V1Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewImplementationRegistry(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = registry.AuditHistorical([]Descriptor{V1Descriptors()[7]}); err != nil {
		t.Fatal(err)
	}
	missing := V1Descriptors()[7]
	missing.Hash = StableDescriptor(missing.ID, missing.Version, "drift").Hash
	if err = registry.AuditHistorical([]Descriptor{missing}); err == nil {
		t.Fatal("expected drift rejection")
	}
}
