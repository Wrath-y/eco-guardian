// Package packageinfo defines the immutable distribution manifest shared by
// assembly, startup verification, and runtime diagnostics.
package packageinfo

import (
	"bytes"
	"encoding/json"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
)

const SchemaVersion = 1

type Mode string

const (
	ModeComplete    Mode = "complete"
	ModeLightweight Mode = "lightweight"
)

type ComponentKind string

const (
	ComponentLocalRAG       ComponentKind = "local-rag"
	ComponentPythonRuntime  ComponentKind = "python-runtime"
	ComponentEmbeddingModel ComponentKind = "embedding-model"
	ComponentRerankModel    ComponentKind = "rerank-model"
)

var requiredCompleteComponents = []ComponentKind{
	ComponentLocalRAG, ComponentPythonRuntime, ComponentEmbeddingModel, ComponentRerankModel,
}

type Manifest struct {
	SchemaVersion        int               `json:"schema_version"`
	PackageMode          Mode              `json:"package_mode"`
	SupportedPlatform    SupportedPlatform `json:"supported_platform"`
	EcoGuardian          EcoIdentity       `json:"eco_guardian"`
	EmbeddedAssetDigests map[string]string `json:"embedded_asset_digests"`
	Components           []Component       `json:"components"`
}

type SupportedPlatform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

type EcoIdentity struct {
	Version                    string `json:"version"`
	Build                      string `json:"build"`
	Commit                     string `json:"commit"`
	RuntimeStatusSchemaVersion string `json:"runtime_status_schema_version"`
	Executable                 string `json:"executable"`
	SizeBytes                  int64  `json:"size_bytes"`
	SHA256                     string `json:"sha256"`
}

type Component struct {
	Kind       ComponentKind `json:"kind"`
	ID         string        `json:"id"`
	Version    string        `json:"version"`
	Entrypoint string        `json:"entrypoint,omitempty"`
	Files      []File        `json:"files"`
}

type File struct {
	Role      string `json:"role"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

var (
	semverPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	tokenPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	hashPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// ValidateShape enforces the v1 generated-document contract without touching
// the filesystem. Startup trust and file verification are separate.
func (m Manifest) ValidateShape() error {
	if m.SchemaVersion != SchemaVersion {
		return diagnostic(CodeManifestCorrupt, "")
	}
	if m.PackageMode != ModeComplete && m.PackageMode != ModeLightweight {
		return diagnostic(CodeModeUnexpected, "")
	}
	if m.SupportedPlatform.OS != "windows" || m.SupportedPlatform.Architecture != "amd64" {
		return diagnostic(CodePlatformUnsupported, "")
	}
	if !semverPattern.MatchString(m.EcoGuardian.Version) || strings.TrimSpace(m.EcoGuardian.Build) == "" || strings.TrimSpace(m.EcoGuardian.Commit) == "" || strings.TrimSpace(m.EcoGuardian.RuntimeStatusSchemaVersion) == "" || !safeRelativePath(m.EcoGuardian.Executable) || m.EcoGuardian.SizeBytes < 0 || !hashPattern.MatchString(m.EcoGuardian.SHA256) {
		if !safeRelativePath(m.EcoGuardian.Executable) {
			return diagnostic(CodePathEscape, "")
		}
		return diagnostic(CodeManifestCorrupt, "")
	}
	if len(m.EmbeddedAssetDigests) == 0 {
		return diagnostic(CodeManifestCorrupt, "")
	}
	for name, digest := range m.EmbeddedAssetDigests {
		if !safeRelativePath(name) {
			return diagnostic(CodePathEscape, "")
		}
		if !hashPattern.MatchString(digest) {
			return diagnostic(CodeManifestCorrupt, "")
		}
	}
	if m.PackageMode == ModeLightweight && len(m.Components) != 0 {
		return diagnostic(CodeModeUnexpected, "")
	}
	if m.Components == nil {
		return diagnostic(CodeManifestCorrupt, "")
	}
	seenKinds := map[ComponentKind]bool{}
	seenPaths := map[string]bool{}
	for _, component := range m.Components {
		if seenKinds[component.Kind] {
			return diagnostic(CodeComponentDuplicate, component.Kind)
		}
		if !component.Kind.Valid() || !tokenPattern.MatchString(component.ID) || strings.TrimSpace(component.Version) == "" || len(component.Files) == 0 {
			return diagnostic(CodeManifestCorrupt, component.Kind)
		}
		if (component.Kind == ComponentLocalRAG || component.Kind == ComponentPythonRuntime) != (component.Entrypoint != "") {
			return diagnostic(CodeManifestCorrupt, component.Kind)
		}
		seenKinds[component.Kind] = true
		entrypointFound := component.Entrypoint == ""
		for _, file := range component.Files {
			if !safeRelativePath(file.Path) {
				return diagnostic(CodePathEscape, component.Kind)
			}
			if seenPaths[file.Path] {
				return diagnostic(CodeComponentDuplicate, component.Kind)
			}
			if !tokenPattern.MatchString(file.Role) || file.SizeBytes < 0 || !hashPattern.MatchString(file.SHA256) {
				return diagnostic(CodeManifestCorrupt, component.Kind)
			}
			seenPaths[file.Path] = true
			entrypointFound = entrypointFound || component.Entrypoint == file.Path
		}
		if !entrypointFound {
			return diagnostic(CodeComponentMissing, component.Kind)
		}
	}
	if m.PackageMode == ModeComplete {
		for _, kind := range requiredCompleteComponents {
			if !seenKinds[kind] {
				return diagnostic(CodeComponentMissing, kind)
			}
		}
		if len(seenKinds) != len(requiredCompleteComponents) {
			return diagnostic(CodeModeUnexpected, "")
		}
	}
	return nil
}

func (k ComponentKind) Valid() bool {
	return k == ComponentLocalRAG || k == ComponentPythonRuntime || k == ComponentEmbeddingModel || k == ComponentRerankModel
}

func safeRelativePath(value string) bool {
	return value != "" && value == path.Clean(value) && value != "." && !strings.HasPrefix(value, "/") && !strings.Contains(value, `\`) && !strings.HasPrefix(value, "../") && !strings.Contains(value, "/../")
}

// CanonicalJSON normalizes unordered manifest collections before encoding.
func (m Manifest) CanonicalJSON() ([]byte, error) {
	if err := m.ValidateShape(); err != nil {
		return nil, err
	}
	copyValue := m
	copyValue.EmbeddedAssetDigests = make(map[string]string, len(m.EmbeddedAssetDigests))
	for name, digest := range m.EmbeddedAssetDigests {
		copyValue.EmbeddedAssetDigests[name] = digest
	}
	copyValue.Components = make([]Component, len(m.Components))
	copy(copyValue.Components, m.Components)
	for index := range copyValue.Components {
		copyValue.Components[index].Files = append([]File(nil), copyValue.Components[index].Files...)
		sort.Slice(copyValue.Components[index].Files, func(left, right int) bool {
			return copyValue.Components[index].Files[left].Path < copyValue.Components[index].Files[right].Path
		})
	}
	sort.Slice(copyValue.Components, func(left, right int) bool {
		return copyValue.Components[left].Kind < copyValue.Components[right].Kind
	})
	return json.Marshal(copyValue)
}

func DecodeStrict(data []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, diagnostic(CodeManifestCorrupt, "")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, diagnostic(CodeManifestCorrupt, "")
	}
	if err := manifest.ValidateShape(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}
