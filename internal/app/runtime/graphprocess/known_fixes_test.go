package graphprocess

import (
	"reflect"
	"testing"
)

func TestKnownFixTableAppliesOnlyToNamedAffectedReleaseLine(t *testing.T) {
	table := KnownFixTable{Version: KnownFixTableVersion, DiagnosticMajor: 1, Rules: []KnownFixRule{{Major: 1, Minor: 4, MinimumPatch: 3, Reason: "SNAPSHOT_HASH_FIX"}}}
	tests := []struct {
		version  string
		allowed  bool
		required string
	}{
		{"1.4.2", false, "1.4.3"},
		{"1.4.3-rc.1", false, "1.4.3"},
		{"1.4.3", true, "1.4.3"},
		{"1.4.9", true, "1.4.3"},
		{"1.3.0", true, ""},
		{"2.0.0", true, ""},
	}
	for _, test := range tests {
		result := table.Evaluate(test.version)
		if result.Allowed != test.allowed || result.ObservedVersion != test.version || result.RequiredVersion != test.required {
			t.Fatalf("version=%s result=%#v", test.version, result)
		}
	}
}

func TestKnownFixTableRecordsMajorDifferenceWithoutRejecting(t *testing.T) {
	table := KnownFixTable{Version: KnownFixTableVersion, DiagnosticMajor: 0, Rules: []KnownFixRule{}}
	result := table.Evaluate("9.1.2")
	if !result.Allowed || !reflect.DeepEqual(result.Diagnostics, []string{"SERVICE_MAJOR_DIFFERENCE"}) {
		t.Fatalf("result=%#v", result)
	}
	invalid := table.Evaluate("development")
	if !invalid.Allowed || !reflect.DeepEqual(invalid.Diagnostics, []string{"SERVICE_VERSION_INVALID"}) {
		t.Fatalf("invalid=%#v", invalid)
	}
}

func TestCompiledKnownFixTableIsVersionedAndSettingsIndependent(t *testing.T) {
	if CompiledKnownFixTable.Version != KnownFixTableVersion || CompiledKnownFixTable.Rules == nil {
		t.Fatalf("compiled table=%#v", CompiledKnownFixTable)
	}
}

func TestKnownFixFloorParticipatesInOtherwiseCompatibleHealth(t *testing.T) {
	health := compatibleHealth()
	health.ServiceVersion = "1.4.2"
	table := KnownFixTable{Version: KnownFixTableVersion, DiagnosticMajor: 1, Rules: []KnownFixRule{{Major: 1, Minor: 4, MinimumPatch: 3, Reason: "SNAPSHOT_HASH_FIX"}}}
	result := evaluateHealthCompatibility(health, nil, table)
	if result.Compatible || result.ObservedVersion != "1.4.2" || result.RequiredVersion != "1.4.3" || !containsString(result.Reasons, "KNOWN_FIX_REQUIRED") || !containsString(result.Diagnostics, "SNAPSHOT_HASH_FIX") {
		t.Fatalf("result=%#v", result)
	}
	health.ServiceVersion = "8.0.0"
	result = evaluateHealthCompatibility(health, nil, table)
	if !result.Compatible || !containsString(result.Diagnostics, "SERVICE_MAJOR_DIFFERENCE") {
		t.Fatalf("major difference rejected: %#v", result)
	}
}
