package contract

import (
	"fmt"
	"testing"
)

func TestV1RegistryFreezesAllManifestKindsAndRejectsDrift(t *testing.T) {
	manifests := V1Manifests()
	registry, err := NewRegistry(manifests)
	if err != nil || len(registry.Manifests()) != 7 {
		t.Fatalf("registry=%#v err=%v", registry, err)
	}
	drifted := manifests[0]
	drifted.SourceHash = manifests[1].SourceHash
	if _, err = NewRegistry(append(manifests, drifted)); err == nil {
		t.Fatal("same-version drift accepted")
	}
	missingGolden := manifests[0]
	missingGolden.GoldenHash = ""
	if _, err = NewRegistry([]Manifest{missingGolden}); err == nil {
		t.Fatal("missing golden accepted")
	}
	registries, err := V1Registries()
	if err != nil || registries.Threshold == nil || registries.Comparison == nil || registries.Cohort == nil || registries.Structural == nil || registries.Report == nil {
		t.Fatalf("registries=%#v err=%v", registries, err)
	}
	for _, manifest := range manifests {
		resolved, found := registry.Resolve(manifest.Kind, manifest.ID, manifest.Version)
		if !found || resolved.SourceHash != manifest.SourceHash {
			t.Fatalf("manifest not resolved: %#v", manifest)
		}
	}
}

func TestRegistryRejectsIncompatibleDirectionUnitAndOverride(t *testing.T) {
	comparison := V1Manifests()[4]
	wrongUnit := comparison
	wrongUnit.Unit = "seconds"
	if _, err := NewRegistry([]Manifest{wrongUnit}); err == nil {
		t.Fatal("comparison with incompatible unit policy accepted")
	}
	wrongOverride := comparison
	wrongOverride.Override = NonOverridable
	if _, err := NewRegistry([]Manifest{wrongOverride}); err == nil {
		t.Fatal("comparison with incompatible override classification accepted")
	}
	wrongDirection := V1Manifests()[0]
	direction := HigherIsRisk
	wrongDirection.Direction = &direction
	if _, err := NewRegistry([]Manifest{wrongDirection}); err == nil {
		t.Fatal("non-comparison manifest with direction accepted")
	}
}

func TestV1ComparisonManifestGoldenHashes(t *testing.T) {
	got := make([]string, 0, 6)
	for _, manifest := range V1Manifests() {
		if manifest.Kind == ComparisonManifest {
			got = append(got, fmt.Sprintf("%s:%s:%s", manifest.ID, manifest.SourceHash, manifest.GoldenHash))
		}
	}
	want := []string{
		"risk-comparison-higher_is_risk:8ff44c3db958e7c53a49c494bed8cb03ea5b03b3690a3e98dd253d82ebfd1c5d:276755d0ad5f092323c6fded42b50902342529325e607243fe3b700f52fb8a74",
		"risk-comparison-lower_is_risk:40110f5443cbe027b5ecd5d5965526e9a91387fc4d9e5495bcb78861f8e0b071:65c1b458fbabf3150c17ec8b8da79a57c64a26ccb3fa609d544e00ff2ee04832",
		"risk-comparison-target_range:cfb11694410b74a77744d8d7a25a41fe115894e4547ee82dc0f24f2675ea5896:c057652427def17406f627bdae38b290889b0f642e37626fdb2dcaa1c87d4b24",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("comparison manifest golden hashes: %#v", got)
	}
}
