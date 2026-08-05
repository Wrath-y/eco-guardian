package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormulaAndValidationHaveNoAdapterDependencies(t *testing.T) {
	for _, dir := range []string{"../formula", "../validation"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, "\"")
				for _, forbidden := range []string{"gin-gonic", "modernc.org/sqlite", "/httpapi", "/storage/sqlite", "/web", "provider", "/ai"} {
					if strings.Contains(path, forbidden) {
						t.Fatalf("%s imports forbidden adapter %s", name, path)
					}
				}
				_ = ast.File{}
			}
		}
	}
}
