package versioning_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDomainPackagesDoNotImportAdaptersOrProviders(t *testing.T) {
	t.Helper()
	forbidden := []string{
		"github.com/gin-gonic/gin", "modernc.org/sqlite", "/internal/storage/sqlite",
		"local-rag", "/simulation", "/risk", "/backup", "openai", "anthropic", "/provider",
	}
	// Enumerate the known bounded domain packages so the test is portable and
	// explicit; filesystem recursive globbing differs across platforms.
	files := []string{}
	for _, pkg := range []string{"revision", "diff", "policy", "gate", "release"} {
		matches, globErr := filepath.Glob(filepath.Join(pkg, "*.go"))
		if globErr != nil {
			t.Fatal(globErr)
		}
		files = append(files, matches...)
	}
	for _, name := range files {
		parsed, parseErr := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		for _, spec := range parsed.Imports {
			path, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				t.Fatalf("unquote %s: %v", name, unquoteErr)
			}
			for _, prefix := range forbidden {
				if strings.Contains(path, prefix) {
					t.Errorf("%s imports forbidden dependency %q", name, path)
				}
			}
		}
	}
}
