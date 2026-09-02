//go:build !windows && !darwin && !linux

package appdir

func resolveHost() (Paths, error) {
	return Paths{}, unsupportedCurrentPlatform()
}
