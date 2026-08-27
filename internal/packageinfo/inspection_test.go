package packageinfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/buildinfo"
)

func TestEmbeddedBuildInventoryContainsWebMigrationsContractsSchemasAndTemplates(t *testing.T) {
	digests, err := buildinfo.EmbeddedAssetDigests()
	if err != nil {
		t.Fatal(err)
	}
	if err = inspectEmbeddedInventory(digests); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteAndLightweightArtifactInspectionsAreClosedAndCredentialFree(t *testing.T) {
	root := t.TempDir()
	completeRequest := completeAssemblyFixture(t, filepath.Join(root, "complete"))
	completeManifest, err := AssembleComplete(context.Background(), completeRequest)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := InspectArtifact(context.Background(), completeRequest.OutputRoot, Expectations{
		PackageMode: ModeComplete, OperatingSystem: "windows", Architecture: "amd64",
		EcoGuardian: completeManifest.EcoGuardian, EmbeddedAssetDigests: completeRequest.EmbeddedAssetDigests,
	})
	if err != nil || complete.PackageMode != ModeComplete || complete.ComponentFiles == 0 || complete.AncillaryFiles < 3 {
		t.Fatalf("inspection=%#v err=%v", complete, err)
	}

	lightweightRequest := lightweightAssemblyFixture(t, filepath.Join(root, "lightweight"))
	lightweightManifest, err := AssembleLightweight(context.Background(), lightweightRequest)
	if err != nil {
		t.Fatal(err)
	}
	lightweight, err := InspectArtifact(context.Background(), lightweightRequest.OutputRoot, Expectations{
		PackageMode: ModeLightweight, OperatingSystem: "windows", Architecture: "amd64",
		EcoGuardian: lightweightManifest.EcoGuardian, EmbeddedAssetDigests: lightweightRequest.EmbeddedAssetDigests,
	})
	if err != nil || lightweight.PackageMode != ModeLightweight || lightweight.ComponentFiles != 0 || lightweight.EmbeddedIdentities != complete.EmbeddedIdentities {
		t.Fatalf("inspection=%#v err=%v", lightweight, err)
	}
}

func TestArtifactInspectionRejectsLLMUndeclaredFilesAndCredentialSettings(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{name: "LLM", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "models/llm/model.gguf", []byte("forbidden"))
		}},
		{name: "undeclared runtime file", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "local-rag/undeclared.dll", []byte("undeclared"))
		}},
		{name: "credential settings", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "defaults/settings.template.json", []byte(`{"schema_version":1,"credential":"secret-canary"}`))
		}},
		{name: "backup contents", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "backups/fixture.ecobackup/project.db", []byte("user database"))
		}},
		{name: "runtime logs", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "logs/runtime.log", []byte("diagnostic"))
		}},
		{name: "test recovery bypass", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "defaults/recovery-bypass.json", []byte(`{"enabled":true}`))
		}},
		{name: "development tools", mutate: func(t *testing.T, root string) {
			writeFixtureFile(t, root, "tools/debug.exe", []byte("debug"))
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := lightweightAssemblyFixture(t, filepath.Join(t.TempDir(), "artifact"))
			manifest, err := AssembleLightweight(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			testCase.mutate(t, request.OutputRoot)
			_, err = InspectArtifact(context.Background(), request.OutputRoot, Expectations{
				PackageMode: ModeLightweight, OperatingSystem: "windows", Architecture: "amd64",
				EcoGuardian: manifest.EcoGuardian, EmbeddedAssetDigests: request.EmbeddedAssetDigests,
			})
			var diagnosticError DiagnosticError
			if !errors.As(err, &diagnosticError) || diagnosticError.Code != CodeContentForbidden && diagnosticError.Code != CodeFileUnexpected {
				t.Fatalf("diagnostic=%#v err=%v", diagnosticError, err)
			}
			if err != nil && (strings.Contains(err.Error(), request.OutputRoot) || strings.Contains(err.Error(), "secret-canary")) {
				t.Fatalf("diagnostic leaked unsafe value: %v", err)
			}
		})
	}
}

func TestAssemblyFailsWhenRequiredEmbeddedIdentityIsAbsent(t *testing.T) {
	request := lightweightAssemblyFixture(t, filepath.Join(t.TempDir(), "artifact"))
	delete(request.EmbeddedAssetDigests, "web/dist/index.html")
	_, err := AssembleLightweight(context.Background(), request)
	var diagnosticError DiagnosticError
	if !errors.As(err, &diagnosticError) || diagnosticError.Code != CodeEmbeddedAssetMismatch {
		t.Fatalf("diagnostic=%#v err=%v", diagnosticError, err)
	}
	if _, statErr := os.Stat(request.OutputRoot); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed assembly left output: %v", statErr)
	}
}
