// Package rootconfig adapts the shared machine settings document and native
// directory-selection capability to backup managed-root selection.
package rootconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/platform/securefs"
	"github.com/zouyi/eco-guardian/internal/project"
)

var (
	ErrSelectionInvalid = errors.New("backup root selection is invalid")
	ErrRootOverlap      = errors.New("backup root overlaps the active project")
)

type SettingsRepository interface {
	Load() (runtimeconfig.Settings, bool, error)
	Save(runtimeconfig.Settings) error
}

type Manager struct {
	Settings    SettingsRepository
	Tokens      project.TokenStore
	DefaultRoot func() (string, error)
	Probe       ports.SpaceProbe
}

var _ ports.ManagedRootSelection = (*Manager)(nil)

func (manager *Manager) IssueSelection(ctx context.Context, selector project.DirectorySelector) (string, time.Time, error) {
	if manager == nil || manager.Tokens == nil || selector == nil {
		return "", time.Time{}, ErrSelectionInvalid
	}
	directory, err := selector.SelectDirectory(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	return manager.Tokens.Issue(directory)
}

func (manager *Manager) ResolveProjectRoot(ctx context.Context, projectID domain.ID) (string, error) {
	if manager == nil || manager.Settings == nil || manager.DefaultRoot == nil || !projectID.Valid() {
		return "", ErrSelectionInvalid
	}
	canonical, err := manager.resolveBase(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(canonical, string(projectID)), nil
}

// ResolveInventoryRoot returns the canonical server-managed base to trusted
// composition adapters. It is never exposed through HTTP.
func (manager *Manager) ResolveInventoryRoot(ctx context.Context) (string, error) {
	if manager == nil || manager.Settings == nil || manager.DefaultRoot == nil {
		return "", ErrSelectionInvalid
	}
	return manager.resolveBase(ctx)
}

// Validate re-probes the configured base without exposing it.
func (manager *Manager) Validate(ctx context.Context) error {
	if manager == nil || manager.Settings == nil || manager.DefaultRoot == nil {
		return ErrSelectionInvalid
	}
	_, err := manager.resolveBase(ctx)
	return err
}

func (manager *Manager) resolveBase(ctx context.Context) (string, error) {
	settings, _, err := manager.Settings.Load()
	if err != nil {
		return "", err
	}
	base := settings.Backup.RootPath
	if settings.Backup.RootMode == runtimeconfig.BackupRootDefault {
		base, err = manager.DefaultRoot()
		if err != nil {
			return "", ErrSelectionInvalid
		}
	}
	canonical, err := privateCanonicalDirectory(base, true)
	if err != nil {
		return "", ErrSelectionInvalid
	}
	if manager.Probe != nil {
		if err = manager.Probe.RequireWritable(ctx, canonical); err != nil {
			return "", err
		}
	}
	return canonical, nil
}

// ApplyNativeSelection consumes a one-use server token and atomically updates
// the existing settings owner. The caller supplies the server-owned active
// project directory only for overlap validation; it is never returned.
func (manager *Manager) ApplyNativeSelection(ctx context.Context, token, activeProjectDirectory string) error {
	if manager == nil || manager.Settings == nil || manager.Tokens == nil || token == "" {
		return ErrSelectionInvalid
	}
	directory, err := manager.Tokens.Consume(token)
	if err != nil {
		return ErrSelectionInvalid
	}
	canonical, err := privateCanonicalDirectory(directory, false)
	if err != nil {
		return ErrSelectionInvalid
	}
	if activeProjectDirectory != "" {
		active, activeErr := canonicalDirectory(activeProjectDirectory, false)
		if activeErr != nil {
			return ErrSelectionInvalid
		}
		if pathsOverlap(canonical, active) {
			return ErrRootOverlap
		}
	}
	if manager.Probe != nil {
		if err = manager.Probe.RequireWritable(ctx, canonical); err != nil {
			return err
		}
	}
	settings, _, err := manager.Settings.Load()
	if err != nil {
		return err
	}
	settings.Backup.RootMode = runtimeconfig.BackupRootCustom
	settings.Backup.RootPath = canonical
	return manager.Settings.Save(settings)
}

func (manager *Manager) ResetDefault(ctx context.Context) error {
	if manager == nil || manager.Settings == nil || manager.DefaultRoot == nil {
		return ErrSelectionInvalid
	}
	root, err := manager.DefaultRoot()
	if err != nil {
		return ErrSelectionInvalid
	}
	canonical, err := privateCanonicalDirectory(root, true)
	if err != nil {
		return ErrSelectionInvalid
	}
	if manager.Probe != nil {
		if err = manager.Probe.RequireWritable(ctx, canonical); err != nil {
			return err
		}
	}
	settings, _, err := manager.Settings.Load()
	if err != nil {
		return err
	}
	settings.Backup.RootMode = runtimeconfig.BackupRootDefault
	settings.Backup.RootPath = ""
	return manager.Settings.Save(settings)
}

func canonicalDirectory(path string, create bool) (string, error) {
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return "", ErrSelectionInvalid
	}
	clean := filepath.Clean(path)
	if create {
		if err := os.MkdirAll(clean, 0o700); err != nil {
			return "", err
		}
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrSelectionInvalid
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Clean(resolved))
}

func privateCanonicalDirectory(path string, create bool) (string, error) {
	canonical, err := canonicalDirectory(path, create)
	if err != nil {
		return "", err
	}
	if err = securefs.Restrict(canonical, true); err != nil {
		return "", ErrSelectionInvalid
	}
	if err = securefs.ValidatePrivate(canonical, true); err != nil {
		return "", ErrSelectionInvalid
	}
	return canonical, nil
}

func pathsOverlap(left, right string) bool {
	return isWithin(left, right) || isWithin(right, left)
}

func isWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative))
}
