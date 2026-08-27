// Package config owns non-secret, machine-local runtime settings.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// SchemaVersion 2 adds the native-selected backup root and count-based
// retention policy. Version 1 remains readable through Store.Load.
const SchemaVersion = 2

type PackageMode string

const (
	PackageDevelopment PackageMode = "development"
	PackageComplete    PackageMode = "complete"
	PackageLightweight PackageMode = "lightweight"
)

type GraphMode string

const (
	GraphDisabled GraphMode = "disabled"
	GraphExternal GraphMode = "external"
	GraphBundled  GraphMode = "bundled"
)

// Settings contains machine preferences only. It deliberately has no token,
// password, credentials, project content, entity, revision, Job, or result
// fields; those facts belong to their owning services or project.db.
type Settings struct {
	SchemaVersion  int             `json:"schema_version"`
	Browser        Browser         `json:"browser"`
	RecentProjects []RecentProject `json:"recent_projects"`
	Package        Package         `json:"package"`
	Graph          Graph           `json:"graph"`
	AI             AIReference     `json:"ai"`
	Logs           LogPolicy       `json:"logs"`
	Backup         BackupDefaults  `json:"backup"`
}

type Browser struct {
	AutoOpen bool `json:"auto_open"`
}

// RecentProject stores only a machine-local project pointer and display label;
// it does not duplicate project contents or business facts.
type RecentProject struct {
	Path  string `json:"path"`
	Label string `json:"label"`
}

type Package struct {
	Mode PackageMode `json:"mode"`
}

type Graph struct {
	Mode                  GraphMode `json:"mode"`
	Endpoint              string    `json:"endpoint"`
	Executable            string    `json:"executable"`
	HealthTimeoutSeconds  int       `json:"health_timeout_seconds"`
	StartupTimeoutSeconds int       `json:"startup_timeout_seconds"`
	RestartLimit          int       `json:"restart_limit"`
}

// AIReference intentionally holds only non-secret provider selection. The
// credential boundary belongs to the applied AI-provider integration.
type AIReference struct {
	Enabled               bool   `json:"enabled"`
	Endpoint              string `json:"endpoint"`
	Model                 string `json:"model"`
	RequestTimeoutSeconds int    `json:"request_timeout_seconds"`
	AllowCloud            bool   `json:"allow_cloud"`
}

type LogPolicy struct {
	MaxBytes int64 `json:"max_bytes"`
	MaxFiles int   `json:"max_files"`
}

type BackupRootMode string

const (
	BackupRootDefault BackupRootMode = "default"
	BackupRootCustom  BackupRootMode = "custom"
)

type BackupDefaults struct {
	// RetentionDays is retained for the runtime-log backup policy introduced by
	// the settings owner. Project database artifact retention is count based and
	// uses the two fields below.
	RetentionDays             int            `json:"retention_days"`
	RootMode                  BackupRootMode `json:"root_mode"`
	RootPath                  string         `json:"root_path,omitempty"`
	DailyRetentionCount       int            `json:"daily_retention_count"`
	ReleaseMigrationRetention int            `json:"release_migration_retention_count"`
}

func Default() Settings {
	return Settings{
		SchemaVersion: SchemaVersion,
		Browser:       Browser{AutoOpen: true},
		Package:       Package{Mode: PackageDevelopment},
		Graph: Graph{
			Mode:                  GraphDisabled,
			HealthTimeoutSeconds:  5,
			StartupTimeoutSeconds: 20,
			RestartLimit:          3,
		},
		AI:   AIReference{RequestTimeoutSeconds: 120},
		Logs: LogPolicy{MaxBytes: 5 << 20, MaxFiles: 5},
		Backup: BackupDefaults{
			RetentionDays:             30,
			RootMode:                  BackupRootDefault,
			DailyRetentionCount:       10,
			ReleaseMigrationRetention: 5,
		},
	}
}

// DecodeStrict accepts exactly one settings JSON object matching this schema.
// Validation of values and persistence behavior are intentionally separate.
func DecodeStrict(data []byte) (Settings, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var settings Settings
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return Settings{}, fmt.Errorf("decode settings: multiple JSON values")
		}
		return Settings{}, fmt.Errorf("decode settings trailing data: %w", err)
	}
	if settings.SchemaVersion != SchemaVersion {
		return Settings{}, fmt.Errorf("unsupported settings schema version %d", settings.SchemaVersion)
	}
	return settings, nil
}
