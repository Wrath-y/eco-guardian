package packageinfo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrAssemblyInvalid = errors.New("package assembly input is invalid")

type ComponentSource struct {
	Kind       ComponentKind
	ID         string
	Version    string
	SourceRoot string
	Entrypoint string
}

type CompleteAssemblyRequest struct {
	OutputRoot           string
	EcoExecutable        string
	EcoGuardian          EcoIdentity
	EmbeddedAssetDigests map[string]string
	Components           []ComponentSource
	DefaultsRoot         string
	LicensesRoot         string
	NoticesRoot          string
	ContractPins         *RuntimeContractPins
}

type LightweightAssemblyRequest struct {
	OutputRoot            string
	EcoExecutable         string
	EcoGuardian           EcoIdentity
	EmbeddedAssetDigests  map[string]string
	ConfigurationTemplate string
	LicensesRoot          string
	NoticesRoot           string
}

var componentDestinations = map[ComponentKind]string{
	ComponentLocalRAG:       "local-rag",
	ComponentPythonRuntime:  "python",
	ComponentEmbeddingModel: "models/embedding",
	ComponentRerankModel:    "models/rerank",
}

var deterministicTimestamp = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// AssembleComplete stages only caller-supplied local files into a new output
// directory. It has no downloader or network port and never replaces an
// existing artifact.
func AssembleComplete(ctx context.Context, request CompleteAssemblyRequest) (Manifest, error) {
	if err := validateCompleteAssembly(request); err != nil {
		return Manifest{}, err
	}
	output, err := prepareAssemblyOutput(request.OutputRoot)
	if err != nil {
		return Manifest{}, err
	}
	parent := filepath.Dir(output)
	temporary, err := os.MkdirTemp(parent, ".eco-complete-*")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(temporary)

	ecoSize, ecoDigest, err := copyRegularFile(ctx, request.EcoExecutable, filepath.Join(temporary, "eco-guardian.exe"), 0o755)
	if err != nil {
		return Manifest{}, err
	}
	components := make([]Component, 0, len(request.Components))
	for _, source := range request.Components {
		component, copyErr := stageComponent(ctx, temporary, source)
		if copyErr != nil {
			return Manifest{}, copyErr
		}
		components = append(components, component)
	}
	for _, ancillary := range []struct{ source, destination string }{
		{request.DefaultsRoot, "defaults"}, {request.LicensesRoot, "licenses"}, {request.NoticesRoot, "notices"},
	} {
		if err = copyTree(ctx, ancillary.source, filepath.Join(temporary, ancillary.destination), nil); err != nil {
			return Manifest{}, err
		}
	}
	manifest := Manifest{
		SchemaVersion: SchemaVersion, PackageMode: ModeComplete,
		SupportedPlatform: SupportedPlatform{OS: "windows", Architecture: "amd64"},
		EcoGuardian:       request.EcoGuardian, EmbeddedAssetDigests: cloneDigests(request.EmbeddedAssetDigests), Components: components,
	}
	manifest.EcoGuardian.Executable = "eco-guardian.exe"
	manifest.EcoGuardian.SizeBytes, manifest.EcoGuardian.SHA256 = ecoSize, ecoDigest
	data, err := manifest.CanonicalJSON()
	if err != nil {
		return Manifest{}, err
	}
	manifestPath := filepath.Join(temporary, ManifestFilename)
	if err = os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		return Manifest{}, err
	}
	if err = os.Chtimes(manifestPath, deterministicTimestamp, deterministicTimestamp); err != nil {
		return Manifest{}, err
	}
	if _, err = inspectLayout(temporary, manifest); err != nil {
		return Manifest{}, err
	}
	if err = normalizeStagedTree(temporary); err != nil {
		return Manifest{}, err
	}
	if err = os.Rename(temporary, output); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// AssembleLightweight produces an offline-first executable package with no
// bundled Graph, Python, or model component directories.
func AssembleLightweight(ctx context.Context, request LightweightAssemblyRequest) (Manifest, error) {
	if err := validateLightweightAssembly(request); err != nil {
		return Manifest{}, err
	}
	output, err := prepareAssemblyOutput(request.OutputRoot)
	if err != nil {
		return Manifest{}, err
	}
	parent := filepath.Dir(output)
	temporary, err := os.MkdirTemp(parent, ".eco-lightweight-*")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(temporary)
	ecoSize, ecoDigest, err := copyRegularFile(ctx, request.EcoExecutable, filepath.Join(temporary, "eco-guardian.exe"), 0o755)
	if err != nil {
		return Manifest{}, err
	}
	if _, _, err = copyRegularFile(ctx, request.ConfigurationTemplate, filepath.Join(temporary, "defaults", "settings.template.json"), 0o644); err != nil {
		return Manifest{}, err
	}
	if err = copyTree(ctx, request.LicensesRoot, filepath.Join(temporary, "licenses"), nil); err != nil {
		return Manifest{}, err
	}
	if err = copyTree(ctx, request.NoticesRoot, filepath.Join(temporary, "notices"), nil); err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		SchemaVersion: SchemaVersion, PackageMode: ModeLightweight,
		SupportedPlatform: SupportedPlatform{OS: "windows", Architecture: "amd64"},
		EcoGuardian:       request.EcoGuardian, EmbeddedAssetDigests: cloneDigests(request.EmbeddedAssetDigests), Components: []Component{},
	}
	manifest.EcoGuardian.Executable = "eco-guardian.exe"
	manifest.EcoGuardian.SizeBytes, manifest.EcoGuardian.SHA256 = ecoSize, ecoDigest
	data, err := manifest.CanonicalJSON()
	if err != nil {
		return Manifest{}, err
	}
	if err = os.WriteFile(filepath.Join(temporary, ManifestFilename), append(data, '\n'), 0o644); err != nil {
		return Manifest{}, err
	}
	if _, err = inspectLayout(temporary, manifest); err != nil {
		return Manifest{}, err
	}
	if err = normalizeStagedTree(temporary); err != nil {
		return Manifest{}, err
	}
	if err = os.Rename(temporary, output); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateLightweightAssembly(request LightweightAssemblyRequest) error {
	if strings.TrimSpace(request.OutputRoot) == "" || strings.TrimSpace(request.EcoExecutable) == "" || strings.TrimSpace(request.ConfigurationTemplate) == "" || strings.TrimSpace(request.LicensesRoot) == "" || strings.TrimSpace(request.NoticesRoot) == "" || len(request.EmbeddedAssetDigests) == 0 {
		return ErrAssemblyInvalid
	}
	if !semverPattern.MatchString(request.EcoGuardian.Version) || strings.TrimSpace(request.EcoGuardian.Build) == "" || strings.TrimSpace(request.EcoGuardian.Commit) == "" || strings.TrimSpace(request.EcoGuardian.RuntimeStatusSchemaVersion) == "" {
		return ErrAssemblyInvalid
	}
	return nil
}

func prepareAssemblyOutput(value string) (string, error) {
	output, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", ErrAssemblyInvalid
	}
	if _, err = os.Lstat(output); err == nil || !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%w: output already exists", ErrAssemblyInvalid)
	}
	if err = os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return "", err
	}
	return output, nil
}

func validateCompleteAssembly(request CompleteAssemblyRequest) error {
	if strings.TrimSpace(request.OutputRoot) == "" || strings.TrimSpace(request.EcoExecutable) == "" || len(request.EmbeddedAssetDigests) == 0 || len(request.Components) != len(requiredCompleteComponents) {
		return ErrAssemblyInvalid
	}
	if !semverPattern.MatchString(request.EcoGuardian.Version) || strings.TrimSpace(request.EcoGuardian.Build) == "" || strings.TrimSpace(request.EcoGuardian.Commit) == "" || strings.TrimSpace(request.EcoGuardian.RuntimeStatusSchemaVersion) == "" {
		return ErrAssemblyInvalid
	}
	if request.ContractPins != nil {
		if request.ContractPins.validateShape() != nil {
			return diagnostic(CodePinSetCorrupt, "")
		}
		for _, component := range request.Components {
			if component.Kind == ComponentLocalRAG {
				if err := VerifyPinnedLocalRAGVersion(*request.ContractPins, component.Version); err != nil {
					return err
				}
			}
		}
	}
	for _, root := range []string{request.DefaultsRoot, request.LicensesRoot, request.NoticesRoot} {
		if strings.TrimSpace(root) == "" {
			return ErrAssemblyInvalid
		}
	}
	seen := map[ComponentKind]bool{}
	for _, component := range request.Components {
		if !component.Kind.Valid() || seen[component.Kind] || !tokenPattern.MatchString(component.ID) || !pinnedVersion(component.Version) || strings.TrimSpace(component.SourceRoot) == "" {
			return ErrAssemblyInvalid
		}
		seen[component.Kind] = true
		requiresEntrypoint := component.Kind == ComponentLocalRAG || component.Kind == ComponentPythonRuntime
		if requiresEntrypoint != (component.Entrypoint != "") || component.Entrypoint != "" && !safeRelativePath(filepath.ToSlash(component.Entrypoint)) {
			return ErrAssemblyInvalid
		}
	}
	for _, kind := range requiredCompleteComponents {
		if !seen[kind] {
			return ErrAssemblyInvalid
		}
	}
	return nil
}

func pinnedVersion(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 {
		return false
	}
	switch strings.ToLower(value) {
	case "latest", "main", "master", "head", "dev", "development":
		return false
	}
	return !strings.ContainsAny(value, "\r\n\x00")
}

func stageComponent(ctx context.Context, staging string, source ComponentSource) (Component, error) {
	destinationRoot := componentDestinations[source.Kind]
	component := Component{Kind: source.Kind, ID: source.ID, Version: source.Version, Files: []File{}}
	err := copyTree(ctx, source.SourceRoot, filepath.Join(staging, filepath.FromSlash(destinationRoot)), func(relative string, size int64, digest string) error {
		packagedPath := path.Join(destinationRoot, filepath.ToSlash(relative))
		role := "resource"
		if source.Kind == ComponentEmbeddingModel || source.Kind == ComponentRerankModel {
			role = "model"
		}
		if source.Entrypoint != "" && filepath.ToSlash(source.Entrypoint) == filepath.ToSlash(relative) {
			role = "executable"
			component.Entrypoint = packagedPath
		}
		component.Files = append(component.Files, File{Role: role, Path: packagedPath, SizeBytes: size, SHA256: digest})
		return nil
	})
	if err != nil {
		return Component{}, err
	}
	if len(component.Files) == 0 || source.Entrypoint != "" && component.Entrypoint == "" {
		return Component{}, ErrAssemblyInvalid
	}
	if component.Entrypoint != "" {
		if err = os.Chmod(filepath.Join(staging, filepath.FromSlash(component.Entrypoint)), 0o755); err != nil {
			return Component{}, err
		}
	}
	sort.Slice(component.Files, func(left, right int) bool { return component.Files[left].Path < component.Files[right].Path })
	return component, nil
}

func normalizeStagedTree(root string) error {
	var names []string
	if err := filepath.WalkDir(root, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		names = append(names, name)
		return nil
	}); err != nil {
		return err
	}
	sort.Slice(names, func(left, right int) bool { return len(names[left]) > len(names[right]) })
	for _, name := range names {
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err = os.Chmod(name, 0o755); err != nil {
				return err
			}
		}
		if err = os.Chtimes(name, deterministicTimestamp, deterministicTimestamp); err != nil {
			return err
		}
	}
	return nil
}

func copyTree(ctx context.Context, sourceRoot, destinationRoot string, record func(string, int64, string) error) error {
	info, err := os.Lstat(sourceRoot)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrAssemblyInvalid
	}
	if err = os.MkdirAll(destinationRoot, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(sourceRoot, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrAssemblyInvalid
		}
		relative, err := filepath.Rel(sourceRoot, name)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return ErrAssemblyInvalid
		}
		if relative == "." {
			return nil
		}
		destination := filepath.Join(destinationRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		if !entry.Type().IsRegular() {
			return ErrAssemblyInvalid
		}
		size, digest, err := copyRegularFile(ctx, name, destination, 0o644)
		if err != nil || record == nil {
			return err
		}
		return record(relative, size, digest)
	})
}

func copyRegularFile(ctx context.Context, source, destination string, mode os.FileMode) (int64, string, error) {
	if err := ctx.Err(); err != nil {
		return 0, "", err
	}
	info, err := os.Lstat(source)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return 0, "", ErrAssemblyInvalid
	}
	input, err := os.Open(source)
	if err != nil {
		return 0, "", err
	}
	defer input.Close()
	if err = os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return 0, "", err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return 0, "", err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hash), &contextReader{ctx: ctx, reader: input})
	if syncErr := output.Sync(); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return 0, "", copyErr
	}
	if err = os.Chtimes(destination, deterministicTimestamp, deterministicTimestamp); err != nil {
		return 0, "", err
	}
	return written, hex.EncodeToString(hash.Sum(nil)), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func cloneDigests(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for name, digest := range values {
		result[name] = digest
	}
	return result
}
