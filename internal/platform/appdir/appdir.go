// Package appdir resolves machine-local Eco Guardian storage without touching
// project data.
package appdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const applicationName = "EcoGuardian"

// Paths contains only machine-local storage. No project database may be
// derived from or stored beneath these paths.
type Paths struct {
	Root     string
	Runtime  string
	Logs     string
	Settings string
}

// UnsupportedPlatformError is safe for presentation in the local launcher.
type UnsupportedPlatformError struct {
	OS   string
	Arch string
}

func (e UnsupportedPlatformError) Error() string {
	return fmt.Sprintf("UNSUPPORTED_PLATFORM: Eco Guardian local runtime supports Windows x64, not %s/%s", e.OS, e.Arch)
}

// ResolveWindows validates a LOCALAPPDATA base and derives canonical child
// locations. It is platform-neutral so its path rules can be verified on every
// development host; ResolveHost supplies the actual Windows environment.
func ResolveWindows(localAppData string) (Paths, error) {
	if strings.TrimSpace(localAppData) == "" {
		return Paths{}, errors.New("LOCALAPPDATA is required")
	}
	base, err := filepath.Abs(filepath.Clean(localAppData))
	if err != nil {
		return Paths{}, fmt.Errorf("canonicalize LOCALAPPDATA: %w", err)
	}
	if !filepath.IsAbs(base) {
		return Paths{}, fmt.Errorf("LOCALAPPDATA must be absolute")
	}
	root := filepath.Join(base, applicationName)
	return Paths{
		Root:     root,
		Runtime:  filepath.Join(root, "runtime"),
		Logs:     filepath.Join(root, "logs"),
		Settings: filepath.Join(root, "settings.json"),
	}, nil
}

// ResolveHost obtains paths for the current OS. Its implementation is selected
// with build tags so unsupported builds always produce an explicit diagnostic.
func ResolveHost() (Paths, error) { return resolveHost() }

// Ensure creates the machine-local directory layout with owner-only requested
// permissions and returns symlink-resolved absolute paths.
func (p Paths) Ensure() (Paths, error) {
	if p.Root == "" || p.Runtime == "" || p.Logs == "" || p.Settings == "" {
		return Paths{}, errors.New("application paths are incomplete")
	}
	for _, directory := range []string{p.Root, p.Runtime, p.Logs} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return Paths{}, fmt.Errorf("create private directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return Paths{}, fmt.Errorf("restrict directory permissions: %w", err)
		}
	}
	root, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		return Paths{}, fmt.Errorf("canonicalize application directory: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve application directory: %w", err)
	}
	return Paths{
		Root:     root,
		Runtime:  filepath.Join(root, "runtime"),
		Logs:     filepath.Join(root, "logs"),
		Settings: filepath.Join(root, "settings.json"),
	}, nil
}

// CreatePrivateFile creates one new root-level machine file without following
// a pre-existing path. Settings persistence later uses the same permissions
// while adding its own atomic replacement protocol.
func (p Paths) CreatePrivateFile(name string) (*os.File, error) {
	if name == "" || filepath.Base(name) != name || name == "." {
		return nil, fmt.Errorf("invalid application file name %q", name)
	}
	paths, err := p.Ensure()
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(paths.Root, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create private application file: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		closeErr := file.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("restrict application file permissions: %w; close: %v", err, closeErr)
		}
		return nil, fmt.Errorf("restrict application file permissions: %w", err)
	}
	return file, nil
}

func unsupportedCurrentPlatform() error {
	return UnsupportedPlatformError{OS: runtime.GOOS, Arch: runtime.GOARCH}
}
