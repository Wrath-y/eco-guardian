//go:build !windows && !darwin && !linux

package credential

func NewWindowsStore() *WindowsStore { return newWindowsStore(nil) }

// NewDefaultStore returns no persistent credential adapter on platforms that
// do not expose one yet. The resolver still supports its non-persistent
// environment fallback.
func NewDefaultStore() *WindowsStore { return NewWindowsStore() }
