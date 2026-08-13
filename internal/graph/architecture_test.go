package graph_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoreGraphPackagesHaveNoAdapterDependencies(t *testing.T) {
	for _, directory := range []string{"projector", "sync", "gate"} {
		files, err := filepath.Glob(filepath.Join(directory, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imported := range file.Imports {
				value := strings.Trim(imported.Path.Value, "\"")
				for _, forbidden := range []string{"gin-gonic", "modernc.org/sqlite", "/httpapi", "/storage/sqlite", "github.com/zouyi/eco-guardian/web", "/graph/client"} {
					if strings.Contains(value, forbidden) {
						t.Fatalf("%s imports forbidden adapter %s", name, value)
					}
				}
			}
		}
	}
}
