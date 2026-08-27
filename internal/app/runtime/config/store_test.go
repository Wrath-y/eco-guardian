package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestStoreLoadsDefaultsAndMigratesV0(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "settings.json"))
	settings, migrated, err := store.Load()
	if err != nil || migrated || !reflect.DeepEqual(settings, Default()) {
		t.Fatalf("missing settings = %#v migrated=%v err=%v", settings, migrated, err)
	}
	if err := os.WriteFile(store.Path(), []byte(`{"schema_version":0,"browser_auto_open":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, migrated, err = store.Load()
	if err != nil || !migrated || settings.SchemaVersion != SchemaVersion || settings.Browser.AutoOpen {
		t.Fatalf("migrated settings = %#v migrated=%v err=%v", settings, migrated, err)
	}
}

func TestStoreMigratesV1WithoutLosingExistingMachineSettings(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "settings.json"))
	v1 := `{"schema_version":1,"browser":{"auto_open":false},"recent_projects":[{"path":"C:\\\\fixture","label":"Fixture"}],"package":{"mode":"lightweight"},"graph":{"mode":"disabled","endpoint":"","executable":"","health_timeout_seconds":7,"startup_timeout_seconds":30,"restart_limit":2},"ai":{"enabled":false,"endpoint":"","model":"","request_timeout_seconds":90,"allow_cloud":false},"logs":{"max_bytes":1048576,"max_files":3},"backup":{"retention_days":42}}`
	if err := os.WriteFile(store.Path(), []byte(v1), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, migrated, err := store.Load()
	if err != nil || !migrated {
		t.Fatalf("migrated=%v err=%v settings=%#v", migrated, err, settings)
	}
	if settings.SchemaVersion != SchemaVersion || settings.Browser.AutoOpen || settings.Package.Mode != PackageLightweight || settings.Backup.RetentionDays != 42 {
		t.Fatalf("v1 values were not preserved: %#v", settings)
	}
	if settings.Backup.RootMode != BackupRootDefault || settings.Backup.DailyRetentionCount != 10 || settings.Backup.ReleaseMigrationRetention != 5 {
		t.Fatalf("v2 backup defaults missing: %#v", settings.Backup)
	}
}

func TestStoreRejectsCorruptAndUnknownVersion(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "settings.json"))
	for _, contents := range [][]byte{[]byte(`{`), []byte(`{"schema_version":9}`)} {
		if err := os.WriteFile(store.Path(), contents, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.Load(); err == nil {
			t.Fatalf("invalid settings %q were accepted", contents)
		}
	}
}

func TestStoreAtomicallySavesAndPreservesPriorFile(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "settings.json"))
	first := Default()
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Browser.AutoOpen = false
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(store.PreviousPath())
	if err != nil {
		t.Fatal(err)
	}
	prior, err := DecodeStrict(previous)
	if err != nil || prior.Browser.AutoOpen != first.Browser.AutoOpen {
		t.Fatalf("prior settings = %#v err=%v", prior, err)
	}
	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("settings mode=%o", info.Mode().Perm())
	}
}

func TestStorePreservesOriginalWhenReplacementFails(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "settings.json"))
	if err := store.Save(Default()); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	store.replace = func(string, string) error { return errors.New("injected replace failure") }
	updated := Default()
	updated.Browser.AutoOpen = false
	if err := store.Save(updated); err == nil {
		t.Fatal("replacement failure was accepted")
	}
	actual, err := os.ReadFile(store.Path())
	if err != nil || string(actual) != string(original) {
		t.Fatalf("settings changed after replacement failure: %q err=%v", actual, err)
	}
}

func TestStoreReportsPathPermissionFailures(t *testing.T) {
	directory := t.TempDir()
	blocked := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewStore(filepath.Join(blocked, "settings.json")).Save(Default()); err == nil {
		t.Fatal("settings write through a non-directory was accepted")
	}
}

func TestStoreSerializesConcurrentWrites(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "settings.json"))
	var group sync.WaitGroup
	errs := make(chan error, 16)
	for index := 0; index < cap(errs); index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			settings := Default()
			settings.Browser.AutoOpen = index%2 == 0
			errs <- store.Save(settings)
		}(index)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := store.Load(); err != nil {
		t.Fatalf("final settings are not readable: %v", err)
	}
}

func TestValidateRequiresLoopbackGraphEndpointsAndSafeBounds(t *testing.T) {
	for _, endpoint := range []string{"http://example.test:8080", "http://192.168.1.10:8080", "http://user:secret@127.0.0.1:8080", "http://127.0.0.1:0"} {
		settings := Default()
		settings.Graph.Mode = GraphExternal
		settings.Graph.Endpoint = endpoint
		err := Validate(settings)
		var validation ValidationError
		if !errors.As(err, &validation) || validation.Field != "graph.endpoint" {
			t.Fatalf("endpoint %q error=%v", endpoint, err)
		}
		problem := ProblemFor(err)
		if problem.Status != 400 || len(problem.Errors) != 1 || problem.Errors[0].Field != "graph.endpoint" || strings.Contains(problem.Errors[0].Message, endpoint) {
			t.Fatalf("unsafe problem for endpoint %q: %#v", endpoint, problem)
		}
	}
	for _, endpoint := range []string{"http://127.0.0.1:8080", "https://localhost:8443", "http://[::1]:8080"} {
		settings := Default()
		settings.Graph.Mode = GraphExternal
		settings.Graph.Endpoint = endpoint
		if err := Validate(settings); err != nil {
			t.Fatalf("loopback endpoint %q error=%v", endpoint, err)
		}
	}
	settings := Default()
	settings.Logs.MaxFiles = 21
	if err := Validate(settings); err == nil {
		t.Fatal("unbounded log setting was accepted")
	}
}

func TestValidateBoundsNonSecretAIProviderSettings(t *testing.T) {
	settings := Default()
	settings.AI.Enabled = true
	if err := Validate(settings); err == nil {
		t.Fatal("enabled AI provider without endpoint/model was accepted")
	}
	settings.AI.Endpoint = "http://127.0.0.1:11434/v1"
	settings.AI.Model = "fixture"
	if err := Validate(settings); err != nil {
		t.Fatalf("bounded non-secret AI settings rejected: %v", err)
	}
	settings.AI.Endpoint = "https://api.example.com/v1"
	if err := Validate(settings); err == nil {
		t.Fatal("cloud AI endpoint without explicit disclosure was accepted")
	}
	settings.AI.AllowCloud = true
	if err := Validate(settings); err != nil {
		t.Fatalf("explicit cloud AI endpoint rejected: %v", err)
	}
	settings.AI.Endpoint = "http://api.example.com/v1"
	if err := Validate(settings); err == nil {
		t.Fatal("plaintext cloud AI endpoint was accepted")
	}
	settings.AI.Endpoint = "http://127.0.0.1:11434/v1"
	settings.AI.AllowCloud = false
	settings.AI.RequestTimeoutSeconds = 601
	if err := Validate(settings); err == nil {
		t.Fatal("unbounded AI timeout was accepted")
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"api_key", "credential_value", "password", "secret"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("AI settings persisted forbidden field %q: %s", forbidden, encoded)
		}
	}
}
