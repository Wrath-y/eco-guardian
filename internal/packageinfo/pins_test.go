package packageinfo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryRuntimeContractPinsAreCompleteAndVerified(t *testing.T) {
	pins, err := LoadAndVerifyRuntimeContractPins(filepath.Join("..", ".."), RuntimeContractPinsPath)
	if err != nil {
		t.Fatal(err)
	}
	if pins.LocalRAG.Version != "0.0.0-dev" || pins.OpenAPI.Version != "1.0.0" || len(pins.ConsumerFixtures) != 3 {
		t.Fatalf("pins=%#v", pins)
	}
}

func TestRuntimeContractPinsReportExplicitDrift(t *testing.T) {
	tests := []struct {
		name string
		path string
		old  string
		new  string
		code DiagnosticCode
	}{
		{
			name: "local-rag version", path: "tests/contract/fixtures/local-rag-graph-service-operability-v1/health.json",
			old: `"service_version":"0.0.0-dev"`, new: `"service_version":"0.0.1"`, code: CodeLocalRAGVersionDrift,
		},
		{
			name: "OpenAPI version", path: "tests/contract/fixtures/local-rag-graph-snapshot-v1/openapi.yaml",
			old: "version: 1.0.0", new: "version: 1.0.1", code: CodeOpenAPIDrift,
		},
		{
			name: "consumer fixture version", path: "tests/contract/fixtures/local-rag-hybrid-graph-retrieval-v1/manifest.json",
			old: `"fixture_version":"1.0"`, new: `"fixture_version":"2.0"`, code: CodeConsumerFixtureDrift,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := copyContractPinFixture(t)
			filename := filepath.Join(root, filepath.FromSlash(test.path))
			body, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			changed := strings.Replace(string(body), test.old, test.new, 1)
			if changed == string(body) {
				t.Fatalf("mutation target not found in %s", test.path)
			}
			if err = os.WriteFile(filename, []byte(changed), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err = LoadAndVerifyRuntimeContractPins(root, RuntimeContractPinsPath)
			assertDiagnosticCode(t, err, test.code)
			if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), test.path) {
				t.Fatalf("diagnostic leaked path: %v", err)
			}
		})
	}
}

func TestCompleteAssemblyRejectsLocalRAGVersionDifferentFromVerifiedPins(t *testing.T) {
	pins, err := LoadAndVerifyRuntimeContractPins(filepath.Join("..", ".."), RuntimeContractPinsPath)
	if err != nil {
		t.Fatal(err)
	}
	request := completeAssemblyFixture(t, filepath.Join(t.TempDir(), "artifact"))
	request.ContractPins = &pins
	request.Components[0].Version = "1.4.2"
	_, err = AssembleComplete(t.Context(), request)
	assertDiagnosticCode(t, err, CodeLocalRAGVersionDrift)
	request.Components[0].Version = pins.LocalRAG.Version
	if _, err = AssembleComplete(t.Context(), request); err != nil {
		t.Fatal(err)
	}
}

func copyContractPinFixture(t *testing.T) string {
	t.Helper()
	sourceRoot := filepath.Join("..", "..")
	destinationRoot := t.TempDir()
	for _, relative := range []string{"packaging/runtime-contract-pins.json", "tests/contract/fixtures"} {
		source := filepath.Join(sourceRoot, filepath.FromSlash(relative))
		err := filepath.WalkDir(source, func(filename string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relativeName, err := filepath.Rel(sourceRoot, filename)
			if err != nil {
				return err
			}
			destination := filepath.Join(destinationRoot, relativeName)
			if entry.IsDir() {
				return os.MkdirAll(destination, 0o755)
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return err
			}
			body, err := os.ReadFile(filename)
			if err != nil {
				return err
			}
			return os.WriteFile(destination, body, 0o644)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return destinationRoot
}

func assertDiagnosticCode(t *testing.T, err error, code DiagnosticCode) {
	t.Helper()
	var diagnosticError DiagnosticError
	if !errors.As(err, &diagnosticError) || diagnosticError.Code != code {
		t.Fatalf("err=%v, want diagnostic %s", err, code)
	}
}
