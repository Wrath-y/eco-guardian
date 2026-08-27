//go:build windows

package backuproot

import "golang.org/x/sys/windows"

func documentsDirectory() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Documents, windows.KF_FLAG_DEFAULT)
}
