package runtime

import (
	"context"
	"errors"
	"testing"

	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
)

type settingsRepositoryFake struct {
	settings runtimeconfig.Settings
	saved    int
	err      error
}

func (f *settingsRepositoryFake) Load() (runtimeconfig.Settings, bool, error) {
	return f.settings, false, f.err
}

func (f *settingsRepositoryFake) Save(settings runtimeconfig.Settings) error {
	if f.err != nil {
		return f.err
	}
	if err := runtimeconfig.Validate(settings); err != nil {
		return err
	}
	f.settings = settings
	f.saved++
	return nil
}

func TestSettingsApplicationAtomicallyUpdatesOneDocumentAndClassifiesEffects(t *testing.T) {
	repository := &settingsRepositoryFake{settings: runtimeconfig.Default()}
	service := SettingsApplication{Repository: repository}
	browser := runtimeconfig.Browser{AutoOpen: false}
	graph := GraphSettingsPatch{Mode: runtimeconfig.GraphExternal, Endpoint: "http://127.0.0.1:9300", HealthTimeoutSeconds: 7, StartupTimeoutSeconds: 30, RestartLimit: 2}
	ai := runtimeconfig.AIReference{Enabled: true, Endpoint: "http://127.0.0.1:11434/v1", Model: "fixture", RequestTimeoutSeconds: 90}
	logs := runtimeconfig.LogPolicy{MaxBytes: 8 << 20, MaxFiles: 4}
	backup := runtimeconfig.BackupDefaults{RetentionDays: 45}

	result, err := service.Update(context.Background(), SettingsPatch{Browser: &browser, Graph: &graph, AI: &ai, Logs: &logs, Backup: &backup})
	if err != nil || repository.saved != 1 || result.Settings.Graph.Endpoint != graph.Endpoint || result.Settings.Graph.Executable != "" {
		t.Fatalf("result=%#v repository=%#v err=%v", result, repository, err)
	}
	want := []SettingsApplyEffect{
		{Field: "browser", Disposition: SettingsApplied},
		{Field: "graph", Disposition: SettingsReconnectRequired},
		{Field: "ai", Disposition: SettingsReconnectRequired},
		{Field: "logs", Disposition: SettingsRestartRequired},
		{Field: "backup", Disposition: SettingsApplied},
	}
	if len(result.Effects) != len(want) {
		t.Fatalf("effects=%#v", result.Effects)
	}
	for index := range want {
		if result.Effects[index] != want[index] {
			t.Fatalf("effect[%d]=%#v want=%#v", index, result.Effects[index], want[index])
		}
	}
}

func TestSettingsApplicationRejectsEmptyOrInvalidPatchWithoutSaving(t *testing.T) {
	repository := &settingsRepositoryFake{settings: runtimeconfig.Default()}
	service := SettingsApplication{Repository: repository}
	if _, err := service.Update(context.Background(), SettingsPatch{}); !errors.Is(err, ErrSettingsPatchEmpty) || repository.saved != 0 {
		t.Fatalf("empty patch err=%v saved=%d", err, repository.saved)
	}
	graph := GraphSettingsPatch{Mode: runtimeconfig.GraphExternal, Endpoint: "http://example.test:9300", HealthTimeoutSeconds: 5, StartupTimeoutSeconds: 20, RestartLimit: 3}
	if _, err := service.Update(context.Background(), SettingsPatch{Graph: &graph}); err == nil || repository.saved != 0 {
		t.Fatalf("invalid patch err=%v saved=%d", err, repository.saved)
	}
}

func TestSettingsApplicationPreservesPackageAndExecutableTrustInputs(t *testing.T) {
	settings := runtimeconfig.Default()
	settings.Package.Mode = runtimeconfig.PackageComplete
	settings.Graph.Executable = `verified/local-rag.exe`
	repository := &settingsRepositoryFake{settings: settings}
	service := SettingsApplication{Repository: repository}
	browser := runtimeconfig.Browser{AutoOpen: false}
	result, err := service.Update(context.Background(), SettingsPatch{Browser: &browser})
	if err != nil || result.Settings.Package != settings.Package || result.Settings.Graph.Executable != settings.Graph.Executable {
		t.Fatalf("trusted settings changed: %#v err=%v", result.Settings, err)
	}
}
