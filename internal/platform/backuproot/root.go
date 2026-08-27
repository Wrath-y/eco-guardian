// Package backuproot resolves the operating-system Documents known folder for
// the default project backup root.
package backuproot

import "path/filepath"

func DefaultRoot() (string, error) {
	documents, err := documentsDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Clean(documents), "EcoGuardian Backups"), nil
}
