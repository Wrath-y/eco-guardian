//go:build windows

package restorejournal

import "github.com/zouyi/eco-guardian/internal/platform/securefs"

func restrictPath(path string, directory bool) error {
	return securefs.Restrict(path, directory)
}

func validateRestrictedPath(path string, directory bool) error {
	return securefs.ValidatePrivate(path, directory)
}

func syncDirectory(path string) error { return securefs.SyncDirectory(path) }
