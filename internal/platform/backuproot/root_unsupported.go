//go:build !windows

package backuproot

import (
	"os"
	"path/filepath"
)

func documentsDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Documents"), nil
}
