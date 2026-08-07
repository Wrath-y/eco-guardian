package revision

import (
	"bytes"
	"testing"
)

func TestManifestCanonicalIdentityIgnoresRegistrationOrder(t *testing.T) {
	entry := func(id string) VersionEntry {
		return VersionEntry{CapabilityID: id, ContractVersion: "1", ImplementationVersion: id + "-v1", State: Registered}
	}
	left := VersionManifest{Entries: []VersionEntry{entry("schema"), entry("dsl")}}
	right := VersionManifest{Entries: []VersionEntry{entry("dsl"), entry("schema")}}
	a, err := left.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, err := right.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("manifest bytes differ: %s != %s", a, b)
	}
	ah, _ := left.Hash()
	bh, _ := right.Hash()
	if ah != bh {
		t.Fatalf("manifest hash differs: %s != %s", ah, bh)
	}
}
