//go:build !windows

package appdir

func resolveHost() (Paths, error) {
	return Paths{}, unsupportedCurrentPlatform()
}
