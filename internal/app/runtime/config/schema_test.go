package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDefaultContainsOnlyMachineRuntimePreferences(t *testing.T) {
	settings := Default()
	if settings.SchemaVersion != SchemaVersion || settings.Package.Mode != PackageDevelopment || settings.Graph.Mode != GraphDisabled || !settings.Browser.AutoOpen {
		t.Fatalf("default settings = %#v", settings)
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"credential", "token", "password", "entity", "revision", "job", "result"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("settings schema contains forbidden %q: %s", forbidden, encoded)
		}
	}
}

func TestDecodeStrictRejectsUnknownAndCredentials(t *testing.T) {
	settings, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeStrict(append(settings[:len(settings)-1], []byte(`,"api_key":"secret"}`)...)); err == nil {
		t.Fatal("credential field was accepted")
	}
	if _, err := DecodeStrict([]byte(`{"schema_version":2}`)); err == nil {
		t.Fatal("unknown schema version was accepted")
	}
}

func TestDecodeStrictRejectsTrailingValues(t *testing.T) {
	settings, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeStrict(append(settings, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing JSON value was accepted")
	}
}
