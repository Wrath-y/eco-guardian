package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRestrictValidateAndSync(t *testing.T) {
	root := filepath.Clean(t.TempDir())
	if err := Restrict(root, true); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivate(root, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "private.dat")
	if err := os.WriteFile(path, []byte("durable"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Restrict(path, false); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivate(path, false); err != nil {
		t.Fatal(err)
	}
	if err := SyncFile(path); err != nil {
		t.Fatal(err)
	}
	if err := SyncDirectory(root); err != nil {
		t.Fatal(err)
	}
}
