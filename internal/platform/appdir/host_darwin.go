//go:build darwin

package appdir

import "os"

// resolveHost uses macOS's per-user Application Support directory. The
// application-data layout remains identical to Windows, but it follows the
// native macOS location returned by os.UserConfigDir.
func resolveHost() (Paths, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}
	return ResolveDarwin(configDir)
}
