package packageinfo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
)

type ArtifactInspection struct {
	PackageMode        Mode
	EmbeddedIdentities int
	ComponentFiles     int
	AncillaryFiles     int
}

// InspectArtifact verifies the trusted manifest first, then proves the staged
// distribution has no undeclared runtime files, LLM assets, or credential
// material in its machine-settings template.
func InspectArtifact(ctx context.Context, root string, expected Expectations) (ArtifactInspection, error) {
	verified, err := LoadAndVerify(ctx, root, expected)
	if err != nil {
		return ArtifactInspection{}, err
	}
	return inspectLayout(verified.Root(), verified.manifest)
}

func inspectLayout(root string, manifest Manifest) (ArtifactInspection, error) {
	if err := inspectEmbeddedInventory(manifest.EmbeddedAssetDigests); err != nil {
		return ArtifactInspection{}, err
	}
	declared := map[string]ComponentKind{}
	for _, component := range manifest.Components {
		for _, file := range component.Files {
			declared[file.Path] = component.Kind
			if forbiddenArtifactName(file.Path, component.Kind) {
				return ArtifactInspection{}, diagnostic(CodeContentForbidden, component.Kind)
			}
		}
	}
	inspection := ArtifactInspection{PackageMode: manifest.PackageMode, EmbeddedIdentities: len(manifest.EmbeddedAssetDigests), ComponentFiles: len(declared)}
	licenseFiles, noticeFiles := 0, 0
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return diagnostic(CodePathEscape, "")
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return diagnostic(CodePathEscape, "")
		}
		relative = filepath.ToSlash(relative)
		if forbiddenArtifactName(relative, declared[relative]) {
			return diagnostic(CodeContentForbidden, declared[relative])
		}
		if relative == ManifestFilename || relative == manifest.EcoGuardian.Executable {
			return nil
		}
		if _, ok := declared[relative]; ok {
			return nil
		}
		switch {
		case strings.HasPrefix(relative, "defaults/"):
			inspection.AncillaryFiles++
		case strings.HasPrefix(relative, "licenses/"):
			inspection.AncillaryFiles++
			licenseFiles++
		case strings.HasPrefix(relative, "notices/"):
			inspection.AncillaryFiles++
			noticeFiles++
		default:
			return diagnostic(CodeFileUnexpected, "")
		}
		return nil
	})
	if err != nil {
		var diagnosticError DiagnosticError
		if errors.As(err, &diagnosticError) {
			return ArtifactInspection{}, err
		}
		return ArtifactInspection{}, diagnostic(CodeFileUnexpected, "")
	}
	if licenseFiles == 0 || noticeFiles == 0 {
		return ArtifactInspection{}, diagnostic(CodeComponentMissing, "")
	}
	settingsData, err := os.ReadFile(filepath.Join(root, "defaults", "settings.template.json"))
	if err != nil {
		return ArtifactInspection{}, diagnostic(CodeComponentMissing, "")
	}
	settings, err := runtimeconfig.DecodeStrict(settingsData)
	if err != nil || runtimeconfig.Validate(settings) != nil {
		return ArtifactInspection{}, diagnostic(CodeContentForbidden, "")
	}
	wantMode := runtimeconfig.PackageMode(manifest.PackageMode)
	if settings.Package.Mode != wantMode {
		return ArtifactInspection{}, diagnostic(CodeModeUnexpected, "")
	}
	return inspection, nil
}

func inspectEmbeddedInventory(digests map[string]string) error {
	required := []string{
		"api/openapi.yaml", "api/package-manifest.schema.json", "web/dist/index.html",
		"compiled/templates/dsl/dsl-v1.ebnf", "compiled/templates/risk/risk-threshold-starter-v1.json",
	}
	for index := 1; index <= 20; index++ {
		required = append(required, fmt.Sprintf("migrations/%04d_", index))
	}
	for _, name := range []string{
		"attribute.schema.json", "character.schema.json", "effect.schema.json", "entity-envelope.schema.json",
		"formula-binding.schema.json", "item.schema.json", "modifier.schema.json", "skill.schema.json",
		"stack-rule.schema.json", "tag.schema.json", "target-selector.schema.json", "trigger-rule.schema.json",
	} {
		required = append(required, "internal/domain/assets/"+name)
	}
	for _, scene := range []string{"single-target-30s", "single-target-180s", "three-target-60s", "extreme-stacking-60s"} {
		required = append(required, "compiled/templates/scenario/"+scene+"@v1.json")
	}
	for _, requiredName := range required {
		found := false
		for name := range digests {
			if name == requiredName || strings.HasPrefix(name, requiredName) {
				found = true
				break
			}
		}
		if !found {
			return diagnostic(CodeEmbeddedAssetMismatch, "")
		}
	}
	hasJavaScript, hasStylesheet := false, false
	for name := range digests {
		lower := strings.ToLower(name)
		hasJavaScript = hasJavaScript || strings.HasPrefix(name, "web/dist/assets/") && strings.HasSuffix(lower, ".js")
		hasStylesheet = hasStylesheet || strings.HasPrefix(name, "web/dist/assets/") && strings.HasSuffix(lower, ".css")
		if forbiddenArtifactName(name, "") {
			return diagnostic(CodeContentForbidden, "")
		}
	}
	if !hasJavaScript || !hasStylesheet {
		return diagnostic(CodeEmbeddedAssetMismatch, "")
	}
	return nil
}

func forbiddenArtifactName(name string, component ComponentKind) bool {
	lower := strings.ToLower(filepath.ToSlash(name))
	for _, segment := range strings.Split(lower, "/") {
		switch segment {
		case ".env", "credential", "credentials", "credential.json", "credentials.json", "secret", "secrets", "secret.json", "secrets.json", "api-key", "api_key", "llm", "chat-model", "large-language-model", "generative-model":
			return true
		}
	}
	extension := strings.ToLower(filepath.Ext(lower))
	if extension == ".gguf" || extension == ".ggml" {
		return true
	}
	if (component == ComponentLocalRAG || component == ComponentPythonRuntime) && (extension == ".onnx" || extension == ".safetensors") {
		return true
	}
	return false
}

func SettingsTemplate(mode Mode) ([]byte, error) {
	settings := runtimeconfig.Default()
	settings.Package.Mode = runtimeconfig.PackageMode(mode)
	if err := runtimeconfig.Validate(settings); err != nil {
		return nil, err
	}
	return json.Marshal(settings)
}
