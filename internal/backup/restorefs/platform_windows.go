//go:build windows

package restorefs

import (
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func renameExact(source, destination string) error {
	from, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 8; attempt++ {
		err = windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "sharing") {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * 15 * time.Millisecond)
	}
	return err
}

func syncDirectory(string) error { return nil }
