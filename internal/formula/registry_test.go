package formula

import "testing"

func TestV1RegistryIsClosedAndDeterministic(t *testing.T) {
	registry, err := V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	if len(UnitsV1) != 9 {
		t.Fatalf("units=%d", len(UnitsV1))
	}
	if _, ok := registry.Unit("damage_point"); !ok {
		t.Fatal("damage_point missing")
	}
	if registry.AllowsBinary(OperationAdd, UnitsV1[0], UnitsV1[1]) {
		t.Fatal("implicit incompatible addition allowed")
	}
	again, err := V1Registry()
	if err != nil || registry.ManifestHash() != again.ManifestHash() {
		t.Fatalf("manifest is nondeterministic: %v", err)
	}
	if _, err := NewRegistry(append(append([]Unit{}, UnitsV1...), UnitsV1[0]), FunctionsV1); err == nil {
		t.Fatal("duplicate unit accepted")
	}
	if _, err := NewRegistry(UnitsV1, append(append([]Function{}, FunctionsV1...), FunctionsV1[0])); err == nil {
		t.Fatal("duplicate function accepted")
	}
	changed := append([]Function(nil), FunctionsV1...)
	changed[0].Signature = "(T)->T"
	if _, err := NewRegistry(UnitsV1, changed); err == nil {
		t.Fatal("changed v1 function signature accepted")
	}
	if err := registry.VerifyManifestHash("wrong"); err == nil {
		t.Fatal("manifest mismatch accepted")
	}
	if _, err := NewRegistry(append(append([]Unit{}, UnitsV1...), Unit{"new", DecimalType, "new", "new"}), FunctionsV1); err == nil {
		t.Fatal("new v1 module accepted without a new version")
	}
	units := registry.Units()
	functions := registry.Functions()
	if len(units) != 9 || len(functions) != 8 || units[0].Name != "control_millisecond" || functions[0].Name != "abs" {
		t.Fatalf("metadata was not complete and sorted: %#v %#v", units, functions)
	}
}
