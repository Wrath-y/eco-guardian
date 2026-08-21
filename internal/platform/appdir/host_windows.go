//go:build windows

package appdir

import "os"

func resolveHost() (Paths, error) {
	return ResolveWindows(os.Getenv("LOCALAPPDATA"))
}
