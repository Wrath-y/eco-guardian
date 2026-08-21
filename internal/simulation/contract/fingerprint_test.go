package contract

import (
	"errors"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

func TestFingerprintPinsRevisionAndRegisteredSimulationComponents(t *testing.T) {
	registry, err := NewManifestRegistry(RequiredV1Descriptors, V1Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	input, err := NormalizeInput(InputRequest{Revision: Revision{ID: "revision", ProjectID: "project", ConfigHash: "config", ManifestHash: "manifest"}, Scene: scenario.BuiltinTemplates()[0], Metrics: []MetricIdentity{{ID: "metric-dps", Version: "v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	revision := []RevisionImplementation{{CapabilityID: "numeric-policy", ContractVersion: "v1", ImplementationVersion: "decimal-v1", State: "registered"}, {CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "schema-v1", State: "registered"}, {CapabilityID: "dsl", ContractVersion: "v1", ImplementationVersion: "dsl-v1", State: "registered"}, {CapabilityID: "validator-registry", ContractVersion: "v1", ImplementationVersion: "registry-v1", State: "registered"}}
	first, firstHash, err := ResolveFingerprint(registry, input, revision)
	if err != nil || len(first.Simulation) != len(RequiredV1Descriptors) || len(firstHash) != 64 {
		t.Fatalf("fingerprint=%#v hash=%q err=%v", first, firstHash, err)
	}
	second, secondHash, err := ResolveFingerprint(registry, input, []RevisionImplementation{revision[3], revision[2], revision[1], revision[0]})
	if err != nil || firstHash != secondHash || len(second.Revision) != len(revision) {
		t.Fatalf("hashes %s %s err=%v", firstHash, secondHash, err)
	}
	revision[0].State = "unregistered"
	if _, _, err = ResolveFingerprint(registry, input, revision); !errors.Is(err, ErrInputInvalid) {
		t.Fatalf("missing registered capability err=%v", err)
	}
}

func TestFingerprintChangesWhenPinnedImplementationChanges(t *testing.T) {
	input, err := NormalizeInput(InputRequest{Revision: Revision{ID: "revision", ProjectID: "project", ConfigHash: "config", ManifestHash: "manifest"}, Scene: scenario.BuiltinTemplates()[0], Metrics: []MetricIdentity{{ID: "metric-dps", Version: "v1"}}})
	if err != nil {
		t.Fatal(err)
	}
	revision := []RevisionImplementation{{CapabilityID: "numeric-policy", ContractVersion: "v1", ImplementationVersion: "decimal-v1", State: "registered"}, {CapabilityID: "schema", ContractVersion: "v1", ImplementationVersion: "schema-v1", State: "registered"}, {CapabilityID: "dsl", ContractVersion: "v1", ImplementationVersion: "dsl-v1", State: "registered"}, {CapabilityID: "validator-registry", ContractVersion: "v1", ImplementationVersion: "registry-v1", State: "registered"}}
	registry, err := NewManifestRegistry(RequiredV1Descriptors, V1Descriptors())
	if err != nil {
		t.Fatal(err)
	}
	_, left, err := ResolveFingerprint(registry, input, revision)
	if err != nil {
		t.Fatal(err)
	}
	revision[0].ImplementationVersion = "decimal-v2"
	_, changedRevision, err := ResolveFingerprint(registry, input, revision)
	if err != nil || left == changedRevision {
		t.Fatalf("revision hashes %s %s err=%v", left, changedRevision, err)
	}
	descriptors := V1Descriptors()
	for index := range descriptors {
		if descriptors[index].ID == "metric-dps" {
			descriptors[index].Hash = strings.Repeat("f", 64)
		}
	}
	changedRegistry, err := NewManifestRegistry(RequiredV1Descriptors, descriptors)
	if err != nil {
		t.Fatal(err)
	}
	_, changedSimulation, err := ResolveFingerprint(changedRegistry, input, revision)
	if err != nil || changedRevision == changedSimulation {
		t.Fatalf("simulation hashes %s %s err=%v", changedRevision, changedSimulation, err)
	}
}
