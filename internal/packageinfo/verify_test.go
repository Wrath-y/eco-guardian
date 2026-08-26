package packageinfo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stagedPackageFixture(t *testing.T) (string, Manifest, Expectations) {
	t.Helper()
	root := t.TempDir()
	manifest := completeManifestFixture()
	for componentIndex := range manifest.Components {
		for fileIndex := range manifest.Components[componentIndex].Files {
			file := &manifest.Components[componentIndex].Files[fileIndex]
			contents := []byte("pinned:" + string(manifest.Components[componentIndex].Kind) + ":" + file.Path)
			writeFixtureFile(t, root, file.Path, contents)
			digest := sha256.Sum256(contents)
			file.SizeBytes = int64(len(contents))
			file.SHA256 = hex.EncodeToString(digest[:])
		}
	}
	ecoContents := []byte("eco-guardian fixture")
	writeFixtureFile(t, root, manifest.EcoGuardian.Executable, ecoContents)
	ecoDigest := sha256.Sum256(ecoContents)
	manifest.EcoGuardian.SizeBytes = int64(len(ecoContents))
	manifest.EcoGuardian.SHA256 = hex.EncodeToString(ecoDigest[:])
	writeManifest(t, root, manifest, true)
	expected := Expectations{
		PackageMode: manifest.PackageMode, OperatingSystem: "windows", Architecture: "amd64",
		EcoGuardian: manifest.EcoGuardian, EmbeddedAssetDigests: manifest.EmbeddedAssetDigests,
	}
	return root, manifest, expected
}

func writeFixtureFile(t *testing.T, root, relative string, contents []byte) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeManifest(t *testing.T, root string, manifest Manifest, canonical bool) {
	t.Helper()
	var data []byte
	var err error
	if canonical {
		data, err = manifest.CanonicalJSON()
	} else {
		data, err = json.Marshal(manifest)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ManifestFilename), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func requireDiagnostic(t *testing.T, err error, code DiagnosticCode, component ComponentKind, forbidden ...string) {
	t.Helper()
	var diagnosticError DiagnosticError
	if !errors.As(err, &diagnosticError) || diagnosticError.Code != code || diagnosticError.Component != component {
		t.Fatalf("diagnostic=%#v err=%v want=%s/%s", diagnosticError, err, code, component)
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(err.Error(), value) {
			t.Fatalf("safe diagnostic leaked %q: %v", value, err)
		}
	}
}

func TestLoadAndVerifyReturnsOnlyVerifiedEntrypoints(t *testing.T) {
	root, manifest, expected := stagedPackageFixture(t)
	verified, err := LoadAndVerify(context.Background(), root, expected)
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Root() != canonicalRoot {
		t.Fatalf("root=%q want=%q", verified.Root(), canonicalRoot)
	}
	for _, kind := range []ComponentKind{ComponentLocalRAG, ComponentPythonRuntime} {
		entrypoint, ok := verified.Entrypoint(kind)
		if !ok || !filepath.IsAbs(entrypoint.AbsolutePath()) || entrypoint.Component != kind || entrypoint.SHA256 == "" {
			t.Fatalf("entrypoint=%#v ok=%v", entrypoint, ok)
		}
	}
	if _, ok := verified.Entrypoint(ComponentEmbeddingModel); ok {
		t.Fatal("model unexpectedly exposed as an executable entrypoint")
	}
	copyValue := verified.Manifest()
	copyValue.EmbeddedAssetDigests["api/openapi.yaml"] = "mutated"
	if verified.Manifest().EmbeddedAssetDigests["api/openapi.yaml"] != manifest.EmbeddedAssetDigests["api/openapi.yaml"] {
		t.Fatal("verified manifest escaped by mutable reference")
	}
}

func TestPackageVerificationStableFailureDiagnostics(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		code      DiagnosticCode
		component ComponentKind
		mutate    func(*testing.T, string, *Manifest, *Expectations)
	}{
		{name: "missing manifest", code: CodeManifestMissing, mutate: func(t *testing.T, root string, _ *Manifest, _ *Expectations) {
			if err := os.Remove(filepath.Join(root, ManifestFilename)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "corrupt manifest", code: CodeManifestCorrupt, mutate: func(t *testing.T, root string, _ *Manifest, _ *Expectations) {
			if err := os.WriteFile(filepath.Join(root, ManifestFilename), []byte(`{"broken"`), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "wrong architecture", code: CodePlatformUnsupported, mutate: func(t *testing.T, root string, manifest *Manifest, _ *Expectations) {
			manifest.SupportedPlatform.Architecture = "arm64"
			writeManifest(t, root, *manifest, false)
		}},
		{name: "unexpected mode", code: CodeModeUnexpected, mutate: func(_ *testing.T, _ string, _ *Manifest, expected *Expectations) {
			expected.PackageMode = ModeLightweight
		}},
		{name: "unexpected mode components", code: CodeModeUnexpected, mutate: func(t *testing.T, root string, manifest *Manifest, expected *Expectations) {
			manifest.PackageMode = ModeLightweight
			expected.PackageMode = ModeLightweight
			writeManifest(t, root, *manifest, false)
		}},
		{name: "duplicate component", code: CodeComponentDuplicate, component: ComponentLocalRAG, mutate: func(t *testing.T, root string, manifest *Manifest, _ *Expectations) {
			manifest.Components[3] = manifest.Components[1]
			writeManifest(t, root, *manifest, false)
		}},
		{name: "path escape", code: CodePathEscape, component: ComponentRerankModel, mutate: func(t *testing.T, root string, manifest *Manifest, _ *Expectations) {
			manifest.Components[0].Files[0].Path = "../outside-canary"
			writeManifest(t, root, *manifest, false)
		}},
		{name: "missing component", code: CodeComponentMissing, component: ComponentRerankModel, mutate: func(t *testing.T, root string, manifest *Manifest, _ *Expectations) {
			if err := os.Remove(filepath.Join(root, filepath.FromSlash(manifest.Components[0].Files[0].Path))); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "corrupt component", code: CodeComponentCorrupt, component: ComponentRerankModel, mutate: func(t *testing.T, root string, manifest *Manifest, _ *Expectations) {
			writeFixtureFile(t, root, manifest.Components[0].Files[0].Path, []byte("tampered model"))
		}},
		{name: "corrupt eco executable", code: CodeComponentCorrupt, mutate: func(t *testing.T, root string, manifest *Manifest, _ *Expectations) {
			writeFixtureFile(t, root, manifest.EcoGuardian.Executable, []byte("tampered executable"))
		}},
		{name: "build mismatch", code: CodeBuildMismatch, mutate: func(_ *testing.T, _ string, _ *Manifest, expected *Expectations) {
			expected.EcoGuardian.Commit = "different"
		}},
		{name: "embedded mismatch", code: CodeEmbeddedAssetMismatch, mutate: func(_ *testing.T, _ string, _ *Manifest, expected *Expectations) {
			expected.EmbeddedAssetDigests = map[string]string{"api/openapi.yaml": strings.Repeat("f", 64)}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root, manifest, expected := stagedPackageFixture(t)
			testCase.mutate(t, root, &manifest, &expected)
			_, err := LoadAndVerify(context.Background(), root, expected)
			requireDiagnostic(t, err, testCase.code, testCase.component, root, "outside-canary")
		})
	}
}

func TestPackageVerificationRejectsManifestSymlink(t *testing.T) {
	root, _, expected := stagedPackageFixture(t)
	manifestPath := filepath.Join(root, ManifestFilename)
	outside := filepath.Join(t.TempDir(), "outside-manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(outside, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(manifestPath); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, manifestPath); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	_, err = LoadAndVerify(context.Background(), root, expected)
	requireDiagnostic(t, err, CodePathEscape, "", root, outside)
}

func TestPackageVerificationRejectsSymlinkEvenWhenTargetExists(t *testing.T) {
	root, manifest, expected := stagedPackageFixture(t)
	file := manifest.Components[0].Files[0]
	outside := filepath.Join(t.TempDir(), "outside-model")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, filepath.FromSlash(file.Path))
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	_, err := LoadAndVerify(context.Background(), root, expected)
	requireDiagnostic(t, err, CodePathEscape, ComponentRerankModel, root, outside)
}

func TestPackageVerificationHonorsCancellation(t *testing.T) {
	root, _, expected := stagedPackageFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LoadAndVerify(ctx, root, expected); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
