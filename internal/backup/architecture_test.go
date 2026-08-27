package backup_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBackupDomainHasNoImplementationImports(t *testing.T) {
	assertNoImplementationImports(t, []string{"domain"}, []string{"gin-gonic", "modernc.org/sqlite", "internal/storage", "internal/platform", "internal/httpapi", "internal/app/runtime", "internal/versioning/release", "internal/graph", "/web"})
}

func TestBackupCoreSupportsOptionalCompositionSubsetsOnlyThroughPorts(t *testing.T) {
	// #5-only, #5+#7, #5+#13 and the fully integrated composition all share
	// these core packages. Concrete project, release, runtime and Graph
	// implementations are allowed only under integration/bootstrap adapters.
	assertNoImplementationImports(t, []string{"domain", "application", "ports"}, []string{
		"gin-gonic", "modernc.org/sqlite", "internal/storage", "internal/platform",
		"internal/httpapi", "internal/app/runtime", "internal/versioning/release",
		"internal/graph", "internal/project", "/web",
	})
}

func assertNoImplementationImports(t *testing.T, directories, forbidden []string) {
	t.Helper()
	for _, directory := range directories {
		entries, err := os.ReadDir(filepath.Clean(directory))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			parsed, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			for _, declaration := range parsed.Decls {
				importDeclaration, ok := declaration.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, specification := range importDeclaration.Specs {
					value, valueErr := strconv.Unquote(specification.(*ast.ImportSpec).Path.Value)
					if valueErr != nil {
						t.Fatal(valueErr)
					}
					for _, fragment := range forbidden {
						if strings.Contains(value, fragment) {
							t.Fatalf("backup core imports optional implementation %q in %s", value, path)
						}
					}
				}
			}
		}
	}
}
