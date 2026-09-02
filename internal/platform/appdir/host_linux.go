//go:build linux

package appdir

import "os"

// resolveHost follows XDG_CONFIG_HOME through os.UserConfigDir. The shared
// layout and permission checks remain identical to the other desktop hosts.
func resolveHost() (Paths, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}
	return ResolveLinux(configDir)
}
