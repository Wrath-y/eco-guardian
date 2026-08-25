package architecture_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormulaAndValidationHaveNoAdapterDependencies(t *testing.T) {
	for _, dir := range []string{"../formula", "../validation", "../rules/materialization"} {
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

func TestRuleMaterializationDoesNotDependOnSimulationOrAdapters(t *testing.T) {
	assertImports(t, "../rules/materialization", func(importPath string) bool {
		return strings.Contains(importPath, "/storage/") || strings.Contains(importPath, "/httpapi") || strings.Contains(importPath, "/simulation/") || strings.Contains(importPath, "/graph") || strings.Contains(importPath, "/ai")
	}, "typed rule materialization must remain a transport-neutral immutable boundary")
}

func TestRuntimeUsesOnlyPortsForModulesAndHTTP(t *testing.T) {
	assertImports(t, "../app/runtime", func(importPath string) bool {
		return strings.HasPrefix(importPath, "github.com/zouyi/eco-guardian/internal/storage/") ||
			importPath == "github.com/zouyi/eco-guardian/internal/httpapi" ||
			strings.HasPrefix(importPath, "github.com/zouyi/eco-guardian/internal/httpapi/")
	}, "runtime must receive module repositories and handlers through ports")
}

func TestBusinessModulesDoNotDependOnWindowsProcessImplementations(t *testing.T) {
	for _, dir := range []string{"../domain", "../formula", "../project", "../validation", "../versioning"} {
		assertImports(t, dir, func(importPath string) bool {
			return importPath == "github.com/zouyi/eco-guardian/internal/platform/windows" ||
				strings.HasPrefix(importPath, "github.com/zouyi/eco-guardian/internal/platform/windows/") ||
				importPath == "github.com/zouyi/eco-guardian/internal/platform/process/windows" ||
				strings.HasPrefix(importPath, "github.com/zouyi/eco-guardian/internal/platform/process/windows/")
		}, "business modules must depend on platform-neutral process ports")
	}
}

func TestSimulationDoesNotDependOnOptionalCapabilities(t *testing.T) {
	assertImports(t, "../simulation", func(importPath string) bool {
		for _, forbidden := range []string{"/graph", "/localrag", "/local-rag", "/impact", "/ai", "model-provider", "/provider"} {
			if strings.Contains(importPath, forbidden) {
				return true
			}
		}
		return false
	}, "simulation must use its transport-neutral ports, not optional capability implementations")
}

func TestSimulationPreviewHasNoJobPersistenceOrTransportDependencies(t *testing.T) {
	assertImports(t, "../simulation/preview", func(importPath string) bool {
		for _, forbidden := range []string{"gin-gonic", "modernc.org/sqlite", "/httpapi", "/storage", "/job", "/app", "/ai", "/versioning/release"} {
			if strings.Contains(importPath, forbidden) {
				return true
			}
		}
		return false
	}, "simulation preview must remain a pure advisory evaluator boundary")
}

func TestSimulationCoreDoesNotIntroduceFloatingPointOrGlobalRandomness(t *testing.T) {
	err := filepath.WalkDir("../simulation", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			value := strings.Trim(imp.Path.Value, "\"")
			if value == "math/rand" || value == "math/rand/v2" {
				t.Errorf("%s imports non-engine random source %s", name, value)
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && (identifier.Name == "float32" || identifier.Name == "float64") {
				t.Errorf("%s introduces %s into simulation core", name, identifier.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRiskPurePackagesDoNotDependOnAdaptersOrOptionalImplementations(t *testing.T) {
	for _, dir := range []string{"../risk/contract", "../risk/threshold", "../risk/cohort", "../risk/comparison", "../risk/structure"} {
		assertImports(t, dir, func(importPath string) bool {
			for _, forbidden := range []string{"gin-gonic", "modernc.org/sqlite", "/httpapi", "/storage/sqlite", "/graph", "/localrag", "/local-rag", "/impact", "/ai", "provider", "/versioning/release"} {
				if strings.Contains(importPath, forbidden) {
					return true
				}
			}
			return false
		}, "risk pure packages must remain transport-neutral and provider-independent")
	}
}

func TestRiskPreviewHasNoJobReportPersistenceOrTransportDependencies(t *testing.T) {
	assertImports(t, "../risk/preview", func(importPath string) bool {
		for _, forbidden := range []string{"gin-gonic", "modernc.org/sqlite", "/httpapi", "/storage", "/job", "/app", "/ai", "/risk/gate", "/risk/orchestration", "/versioning/release"} {
			if strings.Contains(importPath, forbidden) {
				return true
			}
		}
		return false
	}, "risk preview must remain a pure advisory evaluator boundary")
}

func TestAIPureBoundariesDoNotDependOnAdaptersOrMutationWorkers(t *testing.T) {
	for _, dir := range []string{"../ai/contract", "../ai/tools"} {
		assertImports(t, dir, func(importPath string) bool {
			for _, forbidden := range []string{
				"gin-gonic", "modernc.org/sqlite", "vue", "/httpapi", "/storage/sqlite",
				"/ai/provider/openai", "/ai/retrieval/localrag", "/project",
				"/versioning/release", "/graph", "/platform/windows",
			} {
				if strings.Contains(importPath, forbidden) {
					return true
				}
			}
			return false
		}, "AI contract and tool policy code must remain transport-neutral and mutation-free")
	}
}

func TestAIAdapterPackagesRemainOutsidePureBoundaries(t *testing.T) {
	pureDirs := []string{"../ai/contract", "../ai/tools", "../ai/audit"}
	for _, dir := range pureDirs {
		assertImports(t, dir, func(importPath string) bool {
			return strings.Contains(importPath, "/ai/provider/openai") ||
				strings.Contains(importPath, "/ai/retrieval/localrag") ||
				strings.Contains(importPath, "/ai/preview/validation") ||
				strings.Contains(importPath, "/ai/preview/simulation") ||
				strings.Contains(importPath, "/ai/preview/risk")
		}, "pure AI boundaries must depend on ports and contracts, not concrete adapters")
	}
}

func assertImports(t *testing.T, dir string, forbidden func(string) bool, message string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, "\"")
			if forbidden(importPath) {
				t.Errorf("%s imports %s: %s", path.Clean(name), importPath, message)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
