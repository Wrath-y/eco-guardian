//go:build !windows

package restorejournal

import "os"

func atomicReplace(source, destination string) error { return os.Rename(source, destination) }
