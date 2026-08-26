package packageinfo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

const ManifestFilename = "package-manifest.json"
const maxManifestBytes int64 = 8 << 20

type Expectations struct {
	PackageMode          Mode
	OperatingSystem      string
	Architecture         string
	EcoGuardian          EcoIdentity
	EmbeddedAssetDigests map[string]string
}

type VerifiedFile struct {
	Component    ComponentKind
	Role         string
	RelativePath string
	absolutePath string
	SizeBytes    int64
	SHA256       string
}

func (f VerifiedFile) AbsolutePath() string { return f.absolutePath }

type VerifiedPackage struct {
	root       string
	manifest   Manifest
	files      map[string]VerifiedFile
	entrypoint map[ComponentKind]VerifiedFile
}

func (p *VerifiedPackage) Root() string {
	if p == nil {
		return ""
	}
	return p.root
}

func (p *VerifiedPackage) Manifest() Manifest {
	if p == nil {
		return Manifest{}
	}
	return cloneManifest(p.manifest)
}

func (p *VerifiedPackage) Entrypoint(kind ComponentKind) (VerifiedFile, bool) {
	if p == nil {
		return VerifiedFile{}, false
	}
	value, ok := p.entrypoint[kind]
	return value, ok
}

func (p *VerifiedPackage) VerifiedEntrypointPath(kind ComponentKind) (string, bool) {
	file, ok := p.Entrypoint(kind)
	return file.AbsolutePath(), ok
}

func (p *VerifiedPackage) VerifiedComponentRoot(kind ComponentKind) (string, bool) {
	if p == nil || !kind.Valid() {
		return "", false
	}
	if _, exists := p.entrypoint[kind]; !exists && kind != ComponentEmbeddingModel && kind != ComponentRerankModel {
		return "", false
	}
	for _, file := range p.files {
		if file.Component == kind {
			return filepath.Join(p.root, filepath.FromSlash(componentDestinations[kind])), true
		}
	}
	return "", false
}

func LoadAndVerify(ctx context.Context, packageRoot string, expected Expectations) (*VerifiedPackage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(filepath.Clean(packageRoot))
	if err != nil {
		return nil, diagnostic(CodePathEscape, "")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, diagnostic(CodeManifestMissing, "")
	}
	manifestPath := filepath.Join(root, ManifestFilename)
	data, err := readBoundedManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	manifest, err := DecodeStrict(data)
	if err != nil {
		return nil, err
	}
	if manifest.PackageMode != expected.PackageMode {
		return nil, diagnostic(CodeModeUnexpected, "")
	}
	if manifest.SupportedPlatform.OS != expected.OperatingSystem || manifest.SupportedPlatform.Architecture != expected.Architecture {
		return nil, diagnostic(CodePlatformUnsupported, "")
	}
	if !matchesEcoIdentity(manifest.EcoGuardian, expected.EcoGuardian) {
		return nil, diagnostic(CodeBuildMismatch, "")
	}
	if !reflect.DeepEqual(manifest.EmbeddedAssetDigests, expected.EmbeddedAssetDigests) {
		return nil, diagnostic(CodeEmbeddedAssetMismatch, "")
	}
	ecoExecutable, err := resolveRegularFile(root, manifest.EcoGuardian.Executable, "")
	if err != nil {
		return nil, err
	}
	ecoSize, ecoDigest, err := hashFile(ctx, ecoExecutable)
	if err != nil || ecoSize != manifest.EcoGuardian.SizeBytes || ecoDigest != manifest.EcoGuardian.SHA256 {
		return nil, diagnostic(CodeComponentCorrupt, "")
	}

	verified := &VerifiedPackage{root: root, manifest: cloneManifest(manifest), files: map[string]VerifiedFile{}, entrypoint: map[ComponentKind]VerifiedFile{}}
	for _, component := range manifest.Components {
		for _, declared := range component.Files {
			absolute, resolveErr := resolveRegularFile(root, declared.Path, component.Kind)
			if resolveErr != nil {
				return nil, resolveErr
			}
			size, digest, hashErr := hashFile(ctx, absolute)
			if hashErr != nil {
				if errors.Is(hashErr, context.Canceled) || errors.Is(hashErr, context.DeadlineExceeded) {
					return nil, hashErr
				}
				return nil, diagnostic(CodeComponentCorrupt, component.Kind)
			}
			if size != declared.SizeBytes || digest != declared.SHA256 {
				return nil, diagnostic(CodeComponentCorrupt, component.Kind)
			}
			file := VerifiedFile{Component: component.Kind, Role: declared.Role, RelativePath: declared.Path, absolutePath: absolute, SizeBytes: size, SHA256: digest}
			verified.files[declared.Path] = file
			if component.Entrypoint == declared.Path {
				verified.entrypoint[component.Kind] = file
			}
		}
	}
	return verified, nil
}

func matchesEcoIdentity(actual, expected EcoIdentity) bool {
	if actual.Version != expected.Version || actual.Build != expected.Build || actual.Commit != expected.Commit || actual.RuntimeStatusSchemaVersion != expected.RuntimeStatusSchemaVersion || actual.Executable != expected.Executable {
		return false
	}
	if expected.SizeBytes != 0 && actual.SizeBytes != expected.SizeBytes {
		return false
	}
	return expected.SHA256 == "" || actual.SHA256 == expected.SHA256
}

func readBoundedManifest(name string) ([]byte, error) {
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, diagnostic(CodeManifestMissing, "")
	}
	if err != nil {
		return nil, diagnostic(CodeManifestCorrupt, "")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, diagnostic(CodePathEscape, "")
	}
	if !info.Mode().IsRegular() {
		return nil, diagnostic(CodeManifestCorrupt, "")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, diagnostic(CodeManifestCorrupt, "")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil || int64(len(data)) > maxManifestBytes {
		return nil, diagnostic(CodeManifestCorrupt, "")
	}
	return data, nil
}

func resolveRegularFile(root, relative string, component ComponentKind) (string, error) {
	if !safeRelativePath(relative) {
		return "", diagnostic(CodePathEscape, component)
	}
	current := root
	for _, part := range strings.Split(filepath.FromSlash(relative), string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			return "", diagnostic(CodeComponentMissing, component)
		}
		if err != nil {
			return "", diagnostic(CodeComponentCorrupt, component)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", diagnostic(CodePathEscape, component)
		}
	}
	info, err := os.Stat(current)
	if err != nil || !info.Mode().IsRegular() {
		return "", diagnostic(CodeComponentMissing, component)
	}
	relativeToRoot, err := filepath.Rel(root, current)
	if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
		return "", diagnostic(CodePathEscape, component)
	}
	return current, nil
}

func hashFile(ctx context.Context, name string) (int64, string, error) {
	file, err := os.Open(name)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 128<<10)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return 0, "", err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			size += int64(read)
			_, _ = hash.Write(buffer[:read])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return 0, "", readErr
		}
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func cloneManifest(value Manifest) Manifest {
	copyValue := value
	copyValue.EmbeddedAssetDigests = make(map[string]string, len(value.EmbeddedAssetDigests))
	for name, digest := range value.EmbeddedAssetDigests {
		copyValue.EmbeddedAssetDigests[name] = digest
	}
	copyValue.Components = make([]Component, len(value.Components))
	copy(copyValue.Components, value.Components)
	for index := range copyValue.Components {
		copyValue.Components[index].Files = append([]File(nil), value.Components[index].Files...)
	}
	return copyValue
}
