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
	Settings string
}

// UnsupportedPlatformError is safe for presentation in the local launcher.
type UnsupportedPlatformError struct {
	OS   string
	Arch string
}

func (e UnsupportedPlatformError) Error() string {
	return fmt.Sprintf("UNSUPPORTED_PLATFORM: Eco Guardian local runtime supports Windows, macOS, and Linux, not %s/%s", e.OS, e.Arch)
}

// ResolveWindows validates a LOCALAPPDATA base and derives canonical child
// locations. It is platform-neutral so its path rules can be verified on every
// development host; ResolveHost supplies the actual host environment.
func ResolveWindows(localAppData string) (Paths, error) {
	return resolveApplicationData(localAppData, "LOCALAPPDATA")
}

// ResolveDarwin validates a macOS user configuration directory and derives
// the same machine-local child locations as the Windows host adapter. The
// caller supplies os.UserConfigDir() so this path policy stays testable.
func ResolveDarwin(userConfigDir string) (Paths, error) {
	return resolveApplicationData(userConfigDir, "application configuration directory")
}

// ResolveLinux validates an XDG user configuration directory without reading
// the host environment, keeping Linux path policy deterministic in tests.
func ResolveLinux(userConfigDir string) (Paths, error) {
	return resolveApplicationData(userConfigDir, "XDG configuration directory")
}

func resolveApplicationData(baseDir, label string) (Paths, error) {
	if strings.TrimSpace(baseDir) == "" {
		return Paths{}, fmt.Errorf("%s is required", label)
	}
	base, err := filepath.Abs(filepath.Clean(baseDir))
	if err != nil {
		return Paths{}, fmt.Errorf("canonicalize %s: %w", label, err)
	}
	if !filepath.IsAbs(base) {
		return Paths{}, fmt.Errorf("%s must be absolute", label)
	}
	root := filepath.Join(base, applicationName)
	return Paths{
		Root:     root,
		Runtime:  filepath.Join(root, "runtime"),
		Settings: filepath.Join(root, "settings.json"),
	}, nil
}

// ResolveHost obtains paths for the current OS. Its implementation is selected
// with build tags so unsupported builds always produce an explicit diagnostic.
func ResolveHost() (Paths, error) { return resolveHost() }

// Ensure creates the machine-local directory layout with owner-only requested
// permissions and returns symlink-resolved absolute paths.
func (p Paths) Ensure() (Paths, error) {
	if p.Root == "" || p.Runtime == "" || p.Settings == "" {
		return Paths{}, errors.New("application paths are incomplete")
	}
	for _, directory := range []string{p.Root, p.Runtime} {
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
