package backuproot

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultRootIsExternalDocumentsChild(t *testing.T) {
	root, err := DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(root) || filepath.Base(root) != "EcoGuardian Backups" || strings.Contains(root, "project.db") {
		t.Fatalf("unsafe default backup root %q", root)
	}
}
