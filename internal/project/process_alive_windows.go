//go:build windows

package project

import (
	"errors"

	"golang.org/x/sys/windows"
)

const windowsStillActive = 259

func processAlive(pid int) (bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		switch {
		case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
			return false, nil
		case errors.Is(err, windows.ERROR_ACCESS_DENIED):
			return true, nil
		default:
			return false, err
		}
	}
	defer windows.CloseHandle(handle)
	var exitCode uint32
	if err = windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false, err
	}
	return exitCode == windowsStillActive, nil
}
