//go:build !windows

package credential

func NewWindowsStore() *WindowsStore { return newWindowsStore(nil) }
