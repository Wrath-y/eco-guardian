package appdir

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveWindowsDerivesMachineOnlyPaths(t *testing.T) {
	base := t.TempDir()
	paths, err := ResolveWindows(base)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(base, "EcoGuardian")
	if paths.Root != wantRoot || paths.Runtime != filepath.Join(wantRoot, "runtime") || paths.Logs != filepath.Join(wantRoot, "logs") || paths.Settings != filepath.Join(wantRoot, "settings.json") {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestEnsureCanonicalizesAndRestrictsMachinePaths(t *testing.T) {
	paths, err := ResolveWindows(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ensured, err := paths.Ensure()
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{ensured.Root, ensured.Runtime, ensured.Logs} {
		info, err := os.Stat(directory)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("private directory %s has mode %o", directory, info.Mode().Perm())
		}
	}
	file, err := ensured.CreatePrivateFile("settings.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(ensured.Root, "settings.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private file has mode %o", info.Mode().Perm())
	}
}

func TestCreatePrivateFileRejectsTraversal(t *testing.T) {
	paths, err := ResolveWindows(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := paths.CreatePrivateFile("../settings.json"); err == nil {
		t.Fatal("path traversal was accepted")
	}
}

func TestResolveHostReportsUnsupportedPlatforms(t *testing.T) {
	paths, err := ResolveHost()
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatal(err)
		}
		if paths.Root == "" {
			t.Fatal("Windows paths are empty")
		}
		return
	}
	var unsupported UnsupportedPlatformError
	if !errors.As(err, &unsupported) || paths != (Paths{}) {
		t.Fatalf("unsupported result paths=%#v err=%v", paths, err)
	}
}
