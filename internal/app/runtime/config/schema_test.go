package config

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultContainsOnlyMachineRuntimePreferences(t *testing.T) {
	settings := Default()
	if settings.SchemaVersion != SchemaVersion || settings.Package.Mode != PackageDevelopment || settings.Graph.Mode != GraphDisabled || !settings.Browser.AutoOpen {
		t.Fatalf("default settings = %#v", settings)
	}
	if settings.AI.Enabled || settings.AI.RequestTimeoutSeconds != 120 || settings.AI.Endpoint != "" || settings.AI.Model != "" {
		t.Fatalf("unsafe default AI settings = %#v", settings.AI)
	}
	if settings.Backup.RootMode != BackupRootDefault || settings.Backup.RootPath != "" || settings.Backup.DailyRetentionCount != 10 || settings.Backup.ReleaseMigrationRetention != 5 {
		t.Fatalf("unsafe default backup settings = %#v", settings.Backup)
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

func TestBackupSettingsRequireNativeCanonicalRootAndBoundedCounts(t *testing.T) {
	settings := Default()
	settings.Backup.RootMode = BackupRootCustom
	settings.Backup.RootPath = "relative"
	if err := Validate(settings); err == nil {
		t.Fatal("relative custom root was accepted")
	}
	settings.Backup.RootPath = filepath.Clean(t.TempDir())
	if err := Validate(settings); err != nil {
		t.Fatalf("canonical custom root rejected: %v", err)
	}
	settings.Backup.DailyRetentionCount = 0
	if err := Validate(settings); err == nil {
		t.Fatal("zero daily retention was accepted")
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
	if _, err := DecodeStrict([]byte(`{"schema_version":99}`)); err == nil {
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
