package impact

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

func TestDomainAndPlannerImportsRemainTransportNeutral(t *testing.T) {
	for _, directory := range []string{".", "planner"} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imported := range file.Imports {
				value, _ := strconv.Unquote(imported.Path.Value)
				if value == "github.com/gin-gonic/gin" || strings.Contains(value, "/storage/sqlite") || strings.Contains(value, "/graph/client") {
					t.Fatalf("%s imports forbidden adapter %s", path, value)
				}
			}
			ast.Inspect(file, func(ast.Node) bool { return true })
		}
	}
}
