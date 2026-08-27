package packageinfo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func completeAssemblyFixture(t *testing.T, output string) CompleteAssemblyRequest {
	t.Helper()
	sources := filepath.Join(t.TempDir(), "sources")
	makeTree := func(name string, files map[string]string) string {
		root := filepath.Join(sources, name)
		for relative, contents := range files {
			writeFixtureFile(t, root, relative, []byte(contents))
		}
		return root
	}
	executable := filepath.Join(sources, "eco-guardian.exe")
	writeFixtureFile(t, sources, "eco-guardian.exe", []byte("windows executable fixture"))
	settings, err := SettingsTemplate(ModeComplete)
	if err != nil {
		t.Fatal(err)
	}
	return CompleteAssemblyRequest{
		OutputRoot: output, EcoExecutable: executable,
		EcoGuardian:          EcoIdentity{Version: "1.2.3", Build: "release-1", Commit: "abc123", RuntimeStatusSchemaVersion: "1.0", Executable: "ignored-at-assembly"},
		EmbeddedAssetDigests: embeddedInventoryFixture(),
		Components: []ComponentSource{
			{Kind: ComponentLocalRAG, ID: "local-rag", Version: "1.4.2", SourceRoot: makeTree("local-rag", map[string]string{"local-rag.exe": "rag executable", "runtime/config.json": "{}"}), Entrypoint: "local-rag.exe"},
			{Kind: ComponentPythonRuntime, ID: "python-runtime", Version: "3.12.8", SourceRoot: makeTree("python", map[string]string{"python.exe": "python executable", "python312.dll": "python dll"}), Entrypoint: "python.exe"},
			{Kind: ComponentEmbeddingModel, ID: "embedding-model", Version: "model-revision-a1", SourceRoot: makeTree("embedding", map[string]string{"model.onnx": "embedding model", "tokenizer.json": "{}"})},
			{Kind: ComponentRerankModel, ID: "rerank-model", Version: "model-revision-b2", SourceRoot: makeTree("rerank", map[string]string{"model.onnx": "rerank model", "empty.config": ""})},
		},
		DefaultsRoot: makeTree("defaults", map[string]string{"settings.template.json": string(settings)}),
		LicensesRoot: makeTree("licenses", map[string]string{"THIRD-PARTY.txt": "license"}),
		NoticesRoot:  makeTree("notices", map[string]string{"NOTICE.txt": "notice"}),
	}
}

func TestAssembleCompleteStagesLocalInputsAndVerifiedManifest(t *testing.T) {
	output := filepath.Join(t.TempDir(), "eco-complete")
	request := completeAssemblyFixture(t, output)
	manifest, err := AssembleComplete(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		"eco-guardian.exe", ManifestFilename, "local-rag/local-rag.exe", "python/python.exe",
		"models/embedding/model.onnx", "models/rerank/model.onnx", "defaults/settings.template.json",
		"licenses/THIRD-PARTY.txt", "notices/NOTICE.txt",
	} {
		if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("staged file %s: %v", relative, err)
		}
	}
	if manifest.PackageMode != ModeComplete || len(manifest.Components) != 4 || manifest.EcoGuardian.Executable != "eco-guardian.exe" {
		t.Fatalf("manifest=%#v", manifest)
	}
	verified, err := LoadAndVerify(context.Background(), output, Expectations{
		PackageMode: ModeComplete, OperatingSystem: "windows", Architecture: "amd64",
		EcoGuardian: manifest.EcoGuardian, EmbeddedAssetDigests: request.EmbeddedAssetDigests,
	})
	if err != nil {
		t.Fatal(err)
	}
	if entrypoint, ok := verified.Entrypoint(ComponentLocalRAG); !ok || entrypoint.RelativePath != "local-rag/local-rag.exe" {
		t.Fatalf("entrypoint=%#v ok=%v", entrypoint, ok)
	}
	info, err := os.Stat(filepath.Join(output, "eco-guardian.exe"))
	if err != nil {
		t.Fatal(err)
	}
	localRAGInfo, err := os.Stat(filepath.Join(output, "local-rag", "local-rag.exe"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 || localRAGInfo.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable modes eco=%v local-rag=%v", info.Mode(), localRAGInfo.Mode())
	}
	if !info.ModTime().Equal(deterministicTimestamp) || !localRAGInfo.ModTime().Equal(deterministicTimestamp) {
		t.Fatalf("non-deterministic timestamps eco=%v local-rag=%v", info.ModTime(), localRAGInfo.ModTime())
	}
}

func TestCompleteAssemblyIsDeterministicFromIdenticalInputs(t *testing.T) {
	root := t.TempDir()
	firstRequest := completeAssemblyFixture(t, filepath.Join(root, "first"))
	secondRequest := firstRequest
	secondRequest.OutputRoot = filepath.Join(root, "second")
	first, err := AssembleComplete(context.Background(), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AssembleComplete(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := first.CanonicalJSON()
	secondJSON, _ := second.CanonicalJSON()
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("manifest bytes differ\n%s\n%s", firstJSON, secondJSON)
	}
	if left, right := treeDigests(t, firstRequest.OutputRoot), treeDigests(t, secondRequest.OutputRoot); !reflect.DeepEqual(left, right) {
		t.Fatalf("artifact contents differ\n%v\n%v", left, right)
	}
}

func TestCompleteAssemblyRejectsUnpinnedMissingSymlinkAndExistingOutput(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		mutate func(*testing.T, *CompleteAssemblyRequest)
	}{
		{name: "unpinned", mutate: func(_ *testing.T, request *CompleteAssemblyRequest) { request.Components[0].Version = "latest" }},
		{name: "missing notice", mutate: func(_ *testing.T, request *CompleteAssemblyRequest) { request.NoticesRoot = "" }},
		{name: "existing output", mutate: func(t *testing.T, request *CompleteAssemblyRequest) {
			if err := os.MkdirAll(request.OutputRoot, 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "source symlink", mutate: func(t *testing.T, request *CompleteAssemblyRequest) {
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(request.Components[0].SourceRoot, "linked")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := completeAssemblyFixture(t, filepath.Join(t.TempDir(), "artifact"))
			testCase.mutate(t, &request)
			if _, err := AssembleComplete(context.Background(), request); !errors.Is(err, ErrAssemblyInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func lightweightAssemblyFixture(t *testing.T, output string) LightweightAssemblyRequest {
	t.Helper()
	sources := t.TempDir()
	settings, err := SettingsTemplate(ModeLightweight)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, sources, "eco-guardian.exe", []byte("windows executable fixture"))
	writeFixtureFile(t, sources, "settings.template.json", settings)
	writeFixtureFile(t, sources, "licenses/LICENSE.txt", []byte("license"))
	writeFixtureFile(t, sources, "notices/NOTICE.txt", []byte("notice"))
	return LightweightAssemblyRequest{
		OutputRoot: output, EcoExecutable: filepath.Join(sources, "eco-guardian.exe"),
		EcoGuardian:           EcoIdentity{Version: "1.2.3", Build: "release-1", Commit: "abc123", RuntimeStatusSchemaVersion: "1.0"},
		EmbeddedAssetDigests:  embeddedInventoryFixture(),
		ConfigurationTemplate: filepath.Join(sources, "settings.template.json"),
		LicensesRoot:          filepath.Join(sources, "licenses"), NoticesRoot: filepath.Join(sources, "notices"),
	}
}

func embeddedInventoryFixture() map[string]string {
	digest := strings.Repeat("a", 64)
	result := map[string]string{
		"api/openapi.yaml": digest, "api/package-manifest.schema.json": digest,
		"web/dist/index.html": digest, "web/dist/assets/index.js": digest, "web/dist/assets/index.css": digest,
		"compiled/templates/dsl/dsl-v1.ebnf": digest, "compiled/templates/risk/risk-threshold-starter-v1.json": digest,
	}
	for index := 1; index <= 22; index++ {
		result[fmt.Sprintf("migrations/%04d_fixture.sql", index)] = digest
	}
	for _, name := range []string{
		"attribute.schema.json", "character.schema.json", "effect.schema.json", "entity-envelope.schema.json",
		"formula-binding.schema.json", "item.schema.json", "modifier.schema.json", "skill.schema.json",
		"stack-rule.schema.json", "tag.schema.json", "target-selector.schema.json", "trigger-rule.schema.json",
	} {
		result["internal/domain/assets/"+name] = digest
	}
	for _, scene := range []string{"single-target-30s", "single-target-180s", "three-target-60s", "extreme-stacking-60s"} {
		result["compiled/templates/scenario/"+scene+"@v1.json"] = digest
	}
	return result
}

func TestAssembleLightweightContainsNoBundledRuntimeOrModels(t *testing.T) {
	output := filepath.Join(t.TempDir(), "eco-lightweight")
	request := lightweightAssemblyFixture(t, output)
	manifest, err := AssembleLightweight(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.PackageMode != ModeLightweight || manifest.Components == nil || len(manifest.Components) != 0 {
		t.Fatalf("manifest=%#v", manifest)
	}
	for _, relative := range []string{"eco-guardian.exe", ManifestFilename, "defaults/settings.template.json", "licenses/LICENSE.txt", "notices/NOTICE.txt"} {
		if _, err = os.Stat(filepath.Join(output, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("missing lightweight file %s: %v", relative, err)
		}
	}
	for _, forbidden := range []string{"local-rag", "python", "models"} {
		if _, err = os.Stat(filepath.Join(output, forbidden)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("lightweight artifact contains %s: %v", forbidden, err)
		}
	}
	if _, err = LoadAndVerify(context.Background(), output, Expectations{
		PackageMode: ModeLightweight, OperatingSystem: "windows", Architecture: "amd64",
		EcoGuardian: manifest.EcoGuardian, EmbeddedAssetDigests: request.EmbeddedAssetDigests,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLightweightAssemblyIsDeterministicAndRejectsUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	firstRequest := lightweightAssemblyFixture(t, filepath.Join(root, "first"))
	secondRequest := firstRequest
	secondRequest.OutputRoot = filepath.Join(root, "second")
	if _, err := AssembleLightweight(context.Background(), firstRequest); err != nil {
		t.Fatal(err)
	}
	if _, err := AssembleLightweight(context.Background(), secondRequest); err != nil {
		t.Fatal(err)
	}
	if left, right := treeDigests(t, firstRequest.OutputRoot), treeDigests(t, secondRequest.OutputRoot); !reflect.DeepEqual(left, right) {
		t.Fatalf("lightweight contents differ\n%v\n%v", left, right)
	}
	existing := lightweightAssemblyFixture(t, filepath.Join(root, "existing"))
	if err := os.MkdirAll(existing.OutputRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := AssembleLightweight(context.Background(), existing); !errors.Is(err, ErrAssemblyInvalid) {
		t.Fatalf("existing output err=%v", err)
	}
	unsafe := lightweightAssemblyFixture(t, filepath.Join(root, "unsafe"))
	outside := filepath.Join(t.TempDir(), "outside-template")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "template-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	unsafe.ConfigurationTemplate = link
	if _, err := AssembleLightweight(context.Background(), unsafe); !errors.Is(err, ErrAssemblyInvalid) {
		t.Fatalf("symlink input err=%v", err)
	}
}

func treeDigests(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		contents, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = hex.EncodeToString(digest[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
