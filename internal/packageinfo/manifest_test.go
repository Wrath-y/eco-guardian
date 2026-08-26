package packageinfo

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func completeManifestFixture() Manifest {
	hashes := map[string]string{"api/openapi.yaml": strings.Repeat("a", 64), "migrations/0001_initial.sql": strings.Repeat("b", 64)}
	component := func(kind ComponentKind, id, entrypoint, filePath, role, digest string) Component {
		return Component{Kind: kind, ID: id, Version: "1.0-pinned", Entrypoint: entrypoint, Files: []File{{Role: role, Path: filePath, SizeBytes: 128, SHA256: digest}}}
	}
	return Manifest{
		SchemaVersion: SchemaVersion, PackageMode: ModeComplete,
		SupportedPlatform:    SupportedPlatform{OS: "windows", Architecture: "amd64"},
		EcoGuardian:          EcoIdentity{Version: "1.2.3", Build: "release-1", Commit: "abc123", RuntimeStatusSchemaVersion: "1.0", Executable: "eco-guardian.exe", SizeBytes: 42, SHA256: strings.Repeat("e", 64)},
		EmbeddedAssetDigests: hashes,
		Components: []Component{
			component(ComponentRerankModel, "rerank-v1", "", "models/rerank/model.onnx", "model", strings.Repeat("f", 64)),
			component(ComponentLocalRAG, "local-rag-v1", "local-rag/local-rag.exe", "local-rag/local-rag.exe", "executable", strings.Repeat("c", 64)),
			component(ComponentEmbeddingModel, "embedding-v1", "", "models/embedding/model.onnx", "model", strings.Repeat("e", 64)),
			component(ComponentPythonRuntime, "python-3.12", "python/python.exe", "python/python.exe", "executable", strings.Repeat("d", 64)),
		},
	}
}

func TestCompleteAndLightweightManifestShapes(t *testing.T) {
	complete := completeManifestFixture()
	if err := complete.ValidateShape(); err != nil {
		t.Fatal(err)
	}
	lightweight := complete
	lightweight.PackageMode = ModeLightweight
	lightweight.Components = []Component{}
	if err := lightweight.ValidateShape(); err != nil {
		t.Fatal(err)
	}
	lightweight.Components = complete.Components[:1]
	if err := lightweight.ValidateShape(); err == nil {
		t.Fatal("lightweight package accepted a bundled component")
	}
	missing := complete
	missing.Components = missing.Components[:3]
	if err := missing.ValidateShape(); err == nil {
		t.Fatal("complete package accepted a missing pinned component")
	}
}

func TestManifestCanonicalJSONIsDeterministicAndStrict(t *testing.T) {
	manifest := completeManifestFixture()
	first, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	manifest.Components[0], manifest.Components[3] = manifest.Components[3], manifest.Components[0]
	second, err := manifest.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("canonical manifests differ\n%s\n%s", first, second)
	}
	decoded, err := DecodeStrict(first)
	if err != nil || decoded.PackageMode != ModeComplete || len(decoded.Components) != 4 {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	unknown := append([]byte(nil), first[:len(first)-1]...)
	unknown = append(unknown, []byte(`,"unexpected":true}`)...)
	if _, err = DecodeStrict(unknown); err == nil {
		t.Fatal("strict decoder accepted an unknown field")
	}
}

func TestManifestRejectsUnsafeOrAmbiguousPathsAndEntrypoints(t *testing.T) {
	for name, mutate := range map[string]func(*Manifest){
		"path escape":      func(value *Manifest) { value.Components[0].Files[0].Path = "../outside.bin" },
		"absolute":         func(value *Manifest) { value.Components[0].Files[0].Path = "/outside.bin" },
		"duplicate":        func(value *Manifest) { value.Components[1].Files[0].Path = value.Components[0].Files[0].Path },
		"entrypoint":       func(value *Manifest) { value.Components[1].Entrypoint = "local-rag/missing.exe" },
		"model entrypoint": func(value *Manifest) { value.Components[0].Entrypoint = value.Components[0].Files[0].Path },
	} {
		t.Run(name, func(t *testing.T) {
			manifest := completeManifestFixture()
			mutate(&manifest)
			if err := manifest.ValidateShape(); err == nil {
				t.Fatalf("invalid manifest accepted: %#v", manifest)
			}
		})
	}
}

func TestGeneratedSchemaIsCurrentAndValidatesBothModes(t *testing.T) {
	want, err := json.MarshalIndent(SchemaDocument(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	generated, err := os.ReadFile("../../api/package-manifest.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, want) {
		t.Fatal("generated package manifest schema is stale; run go generate ./internal/packageinfo")
	}

	schemaValue, err := jsonschema.UnmarshalJSON(bytes.NewReader(generated))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("package-manifest.schema.json", schemaValue); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("package-manifest.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	complete := completeManifestFixture()
	completeJSON, err := complete.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(completeJSON))
	schemaErr := schema.Validate(instance)
	if err != nil || schemaErr != nil {
		t.Fatalf("complete schema validation decode=%v schema=%v", err, schemaErr)
	}
	lightweight := complete
	lightweight.PackageMode = ModeLightweight
	lightweight.Components = []Component{}
	lightweightJSON, err := lightweight.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	instance, err = jsonschema.UnmarshalJSON(bytes.NewReader(lightweightJSON))
	schemaErr = schema.Validate(instance)
	if err != nil || schemaErr != nil {
		t.Fatalf("lightweight schema validation decode=%v schema=%v", err, schemaErr)
	}

	missing := complete
	missing.Components = missing.Components[:3]
	raw, err := json.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	instance, err = jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if schema.Validate(instance) == nil {
		t.Fatal("schema accepted complete package without all four component kinds")
	}
}

func TestRequiredCompleteComponentKindsRemainClosed(t *testing.T) {
	want := []ComponentKind{ComponentLocalRAG, ComponentPythonRuntime, ComponentEmbeddingModel, ComponentRerankModel}
	if !reflect.DeepEqual(requiredCompleteComponents, want) {
		t.Fatalf("required kinds=%v", requiredCompleteComponents)
	}
}
