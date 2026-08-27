//go:build !windows

package restorefs

import (
	"os"
)

func renameExact(source, destination string) error { return os.Rename(source, destination) }

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	return closeErr
}
