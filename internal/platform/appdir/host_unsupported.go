//go:build !windows && !darwin

package appdir

func resolveHost() (Paths, error) {
	return Paths{}, unsupportedCurrentPlatform()
}
