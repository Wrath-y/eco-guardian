package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
)

const previousSuffix = ".previous"

var settingsWriteMu sync.Mutex

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string { return e.Field + ": " + e.Message }

// Validate enforces schema-local bounds. Network/loopback policy is evaluated
// separately at runtime admission, so this function has no external effects.
func Validate(settings Settings) error {
	if settings.SchemaVersion != SchemaVersion {
		return ValidationError{Field: "schema_version", Message: "unsupported version"}
	}
	if settings.Package.Mode != PackageDevelopment && settings.Package.Mode != PackageComplete && settings.Package.Mode != PackageLightweight {
		return ValidationError{Field: "package.mode", Message: "unsupported package mode"}
	}
	if settings.Graph.Mode != GraphDisabled && settings.Graph.Mode != GraphExternal && settings.Graph.Mode != GraphBundled {
		return ValidationError{Field: "graph.mode", Message: "unsupported graph mode"}
	}
	if settings.Graph.Mode == GraphExternal && settings.Graph.Endpoint == "" {
		return ValidationError{Field: "graph.endpoint", Message: "is required for an external service"}
	}
	if settings.Graph.Endpoint != "" {
		if err := validateLoopbackEndpoint(settings.Graph.Endpoint); err != nil {
			return err
		}
	}
	if settings.Graph.HealthTimeoutSeconds < 1 || settings.Graph.HealthTimeoutSeconds > 60 {
		return ValidationError{Field: "graph.health_timeout_seconds", Message: "must be between 1 and 60"}
	}
	if settings.Graph.StartupTimeoutSeconds < 1 || settings.Graph.StartupTimeoutSeconds > 300 {
		return ValidationError{Field: "graph.startup_timeout_seconds", Message: "must be between 1 and 300"}
	}
	if settings.Graph.RestartLimit < 0 || settings.Graph.RestartLimit > 10 {
		return ValidationError{Field: "graph.restart_limit", Message: "must be between 0 and 10"}
	}
	if settings.AI.Enabled && (settings.AI.Endpoint == "" || settings.AI.Model == "") {
		return ValidationError{Field: "ai", Message: "enabled provider requires endpoint and model"}
	}
	if settings.AI.Endpoint != "" {
		if _, err := aiprovider.ValidateEndpoint(settings.AI.Endpoint, settings.AI.AllowCloud); err != nil {
			return ValidationError{Field: "ai.endpoint", Message: "must be loopback HTTP(S) or explicitly allowed cloud HTTPS"}
		}
	}
	if len(settings.AI.Model) > 256 || strings.TrimSpace(settings.AI.Model) != settings.AI.Model || strings.ContainsAny(settings.AI.Model, "\r\n\x00") {
		return ValidationError{Field: "ai.model", Message: "is invalid"}
	}
	if settings.AI.RequestTimeoutSeconds != 0 && (settings.AI.RequestTimeoutSeconds < 1 || settings.AI.RequestTimeoutSeconds > 600) {
		return ValidationError{Field: "ai.request_timeout_seconds", Message: "must be between 1 and 600"}
	}
	if settings.AI.Enabled && settings.AI.RequestTimeoutSeconds == 0 {
		return ValidationError{Field: "ai.request_timeout_seconds", Message: "is required when AI is enabled"}
	}
	if settings.Logs.MaxBytes < 64<<10 || settings.Logs.MaxBytes > 1<<30 {
		return ValidationError{Field: "logs.max_bytes", Message: "must be between 65536 and 1073741824"}
	}
	if settings.Logs.MaxFiles < 1 || settings.Logs.MaxFiles > 20 {
		return ValidationError{Field: "logs.max_files", Message: "must be between 1 and 20"}
	}
	if settings.Backup.RetentionDays < 0 || settings.Backup.RetentionDays > 3650 {
		return ValidationError{Field: "backup.retention_days", Message: "must be between 0 and 3650"}
	}
	return nil
}

func validateLoopbackEndpoint(value string) error {
	endpoint, err := url.ParseRequestURI(value)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return ValidationError{Field: "graph.endpoint", Message: "must be an absolute loopback URL"}
	}
	if endpoint.User != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return ValidationError{Field: "graph.endpoint", Message: "must be an unauthenticated http(s) loopback URL"}
	}
	host := endpoint.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return ValidationError{Field: "graph.endpoint", Message: "must use a loopback host"}
		}
	}
	if port := endpoint.Port(); port != "" {
		portNumber, convertErr := strconv.Atoi(port)
		if convertErr != nil || portNumber < 1 || portNumber > 65535 {
			return ValidationError{Field: "graph.endpoint", Message: "has an invalid port"}
		}
	}
	return nil
}

// Store persists exactly one settings document outside project.db.
type Store struct {
	path    string
	replace func(string, string) error
}

func NewStore(path string) *Store {
	return &Store{path: path, replace: os.Rename}
}

func (s *Store) Path() string { return s.path }

func (s *Store) PreviousPath() string { return s.path + previousSuffix }

// Load returns defaults when settings have not yet been created. The bool is
// true only when a supported older schema was migrated in memory.
func (s *Store) Load() (Settings, bool, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), false, nil
	}
	if err != nil {
		return Settings{}, false, fmt.Errorf("read settings: %w", err)
	}
	settings, migrated, err := decodeWithMigration(data)
	if err != nil {
		return Settings{}, false, err
	}
	if err := Validate(settings); err != nil {
		return Settings{}, false, err
	}
	return settings, migrated, nil
}

func decodeWithMigration(data []byte) (Settings, bool, error) {
	var header struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return Settings{}, false, fmt.Errorf("decode settings header: %w", err)
	}
	switch header.SchemaVersion {
	case SchemaVersion:
		settings, err := DecodeStrict(data)
		return settings, false, err
	case 0:
		var legacy struct {
			SchemaVersion   int  `json:"schema_version"`
			BrowserAutoOpen bool `json:"browser_auto_open"`
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&legacy); err != nil {
			return Settings{}, false, fmt.Errorf("decode legacy settings: %w", err)
		}
		if legacy.SchemaVersion != 0 {
			return Settings{}, false, fmt.Errorf("unsupported settings schema version %d", legacy.SchemaVersion)
		}
		settings := Default()
		settings.Browser.AutoOpen = legacy.BrowserAutoOpen
		return settings, true, nil
	default:
		return Settings{}, false, fmt.Errorf("unsupported settings schema version %d", header.SchemaVersion)
	}
}

// Save flushes a restrictive temporary file and atomically replaces settings.
// Existing settings are copied to a sibling recovery file before replacement.
func (s *Store) Save(settings Settings) error {
	if err := Validate(settings); err != nil {
		return err
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	settingsWriteMu.Lock()
	defer settingsWriteMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("restrict settings directory: %w", err)
	}
	if err := preservePriorFile(s.path, s.PreviousPath()); err != nil {
		return err
	}
	temporary, err := writeTemporary(filepath.Dir(s.path), data)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := s.replace(temporary, s.path); err != nil {
		return fmt.Errorf("atomically replace settings: %w", err)
	}
	return nil
}

func preservePriorFile(path, previous string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read prior settings: %w", err)
	}
	temporary, err := writeTemporary(filepath.Dir(previous), data)
	if err != nil {
		return err
	}
	defer os.Remove(temporary)
	if err := os.Rename(temporary, previous); err != nil {
		return fmt.Errorf("preserve prior settings: %w", err)
	}
	return nil
}

func writeTemporary(directory string, data []byte) (string, error) {
	file, err := os.CreateTemp(directory, ".settings-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temporary settings: %w", err)
	}
	name := file.Name()
	closeWithError := func(cause error) (string, error) {
		_ = file.Close()
		_ = os.Remove(name)
		return "", cause
	}
	if err := file.Chmod(0o600); err != nil {
		return closeWithError(fmt.Errorf("restrict temporary settings: %w", err))
	}
	if _, err := file.Write(data); err != nil {
		return closeWithError(fmt.Errorf("write temporary settings: %w", err))
	}
	if err := file.Sync(); err != nil {
		return closeWithError(fmt.Errorf("flush temporary settings: %w", err))
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("close temporary settings: %w", err)
	}
	return name, nil
}
