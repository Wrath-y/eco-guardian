package runtime

import (
	"context"
	"errors"

	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
)

var (
	ErrSettingsUnavailable = errors.New("runtime settings are unavailable")
	ErrSettingsPatchEmpty  = errors.New("runtime settings patch is empty")
)

type SettingsRepository interface {
	Load() (runtimeconfig.Settings, bool, error)
	Save(runtimeconfig.Settings) error
}

type GraphSettingsPatch struct {
	Mode                  runtimeconfig.GraphMode
	Endpoint              string
	HealthTimeoutSeconds  int
	StartupTimeoutSeconds int
	RestartLimit          int
}

// SettingsPatch is deliberately limited to the non-secret settings exposed by
// the shared settings resource. Package identity and the bundled executable
// path are verified startup inputs and cannot be changed through this API.
type SettingsPatch struct {
	Browser *runtimeconfig.Browser
	Graph   *GraphSettingsPatch
	AI      *runtimeconfig.AIReference
	Logs    *runtimeconfig.LogPolicy
	Backup  *runtimeconfig.BackupDefaults
}

func (p SettingsPatch) Empty() bool {
	return p.Browser == nil && p.Graph == nil && p.AI == nil && p.Logs == nil && p.Backup == nil
}

type SettingsApplyDisposition string

const (
	SettingsApplied           SettingsApplyDisposition = "applied"
	SettingsReconnectRequired SettingsApplyDisposition = "reconnect_required"
	SettingsRestartRequired   SettingsApplyDisposition = "restart_required"
)

type SettingsApplyEffect struct {
	Field       string
	Disposition SettingsApplyDisposition
}

type SettingsUpdate struct {
	Settings runtimeconfig.Settings
	Effects  []SettingsApplyEffect
}

// SettingsApplication is the single application-level owner for loading and
// atomically updating machine settings. It classifies runtime effects without
// reaching into process supervisors, credentials, or project stores.
type SettingsApplication struct{ Repository SettingsRepository }

func (s SettingsApplication) Read(ctx context.Context) (runtimeconfig.Settings, error) {
	if err := ctx.Err(); err != nil {
		return runtimeconfig.Settings{}, err
	}
	if s.Repository == nil {
		return runtimeconfig.Settings{}, ErrSettingsUnavailable
	}
	settings, _, err := s.Repository.Load()
	if err != nil {
		return runtimeconfig.Settings{}, errors.Join(ErrSettingsUnavailable, err)
	}
	return settings, nil
}

func (s SettingsApplication) Update(ctx context.Context, patch SettingsPatch) (SettingsUpdate, error) {
	if patch.Empty() {
		return SettingsUpdate{}, ErrSettingsPatchEmpty
	}
	current, err := s.Read(ctx)
	if err != nil {
		return SettingsUpdate{}, err
	}
	next := current
	if patch.Browser != nil {
		next.Browser = *patch.Browser
	}
	if patch.Graph != nil {
		next.Graph.Mode = patch.Graph.Mode
		next.Graph.Endpoint = patch.Graph.Endpoint
		next.Graph.HealthTimeoutSeconds = patch.Graph.HealthTimeoutSeconds
		next.Graph.StartupTimeoutSeconds = patch.Graph.StartupTimeoutSeconds
		next.Graph.RestartLimit = patch.Graph.RestartLimit
	}
	if patch.AI != nil {
		next.AI = *patch.AI
	}
	if patch.Logs != nil {
		next.Logs = *patch.Logs
	}
	if patch.Backup != nil {
		// Backup settings are patched field-by-field so the public retention
		// resource cannot accidentally erase the server-owned native root.
		if patch.Backup.RetentionDays != 0 {
			next.Backup.RetentionDays = patch.Backup.RetentionDays
		}
		if patch.Backup.RootMode != "" {
			next.Backup.RootMode = patch.Backup.RootMode
			next.Backup.RootPath = patch.Backup.RootPath
		}
		if patch.Backup.DailyRetentionCount != 0 {
			next.Backup.DailyRetentionCount = patch.Backup.DailyRetentionCount
		}
		if patch.Backup.ReleaseMigrationRetention != 0 {
			next.Backup.ReleaseMigrationRetention = patch.Backup.ReleaseMigrationRetention
		}
	}
	if err := ctx.Err(); err != nil {
		return SettingsUpdate{}, err
	}
	if err := s.Repository.Save(next); err != nil {
		return SettingsUpdate{}, err
	}
	return SettingsUpdate{Settings: next, Effects: settingsEffects(current, next)}, nil
}

func settingsEffects(before, after runtimeconfig.Settings) []SettingsApplyEffect {
	effects := make([]SettingsApplyEffect, 0, 5)
	if before.Browser != after.Browser {
		effects = append(effects, SettingsApplyEffect{Field: "browser", Disposition: SettingsApplied})
	}
	if before.Graph.Mode != after.Graph.Mode || before.Graph.Endpoint != after.Graph.Endpoint || before.Graph.HealthTimeoutSeconds != after.Graph.HealthTimeoutSeconds || before.Graph.StartupTimeoutSeconds != after.Graph.StartupTimeoutSeconds || before.Graph.RestartLimit != after.Graph.RestartLimit {
		effects = append(effects, SettingsApplyEffect{Field: "graph", Disposition: SettingsReconnectRequired})
	}
	if before.AI != after.AI {
		effects = append(effects, SettingsApplyEffect{Field: "ai", Disposition: SettingsReconnectRequired})
	}
	if before.Logs != after.Logs {
		effects = append(effects, SettingsApplyEffect{Field: "logs", Disposition: SettingsRestartRequired})
	}
	if before.Backup != after.Backup {
		effects = append(effects, SettingsApplyEffect{Field: "backup", Disposition: SettingsApplied})
	}
	return effects
}
