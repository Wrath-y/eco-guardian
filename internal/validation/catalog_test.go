package validation

import "testing"

func TestV1CatalogRejectsDrift(t *testing.T) {
	if err := ValidateCodesV1(CodesV1); err != nil {
		t.Fatal(err)
	}
	changed := append([]Code(nil), CodesV1...)
	changed[0].Severity = SeverityBlock
	if err := ValidateCodesV1(changed); err == nil {
		t.Fatal("severity drift accepted")
	}
	if _, err := CodeManifestHash(append(append([]Code{}, CodesV1...), CodesV1[0])); err == nil {
		t.Fatal("duplicate code accepted")
	}
}
