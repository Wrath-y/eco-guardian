// Package buildinfo exposes the immutable identity of an Eco Guardian binary.
package buildinfo

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"

	ecoguardian "github.com/zouyi/eco-guardian"
	"github.com/zouyi/eco-guardian/internal/formula"
	riskthreshold "github.com/zouyi/eco-guardian/internal/risk/threshold"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

const RuntimeStatusSchemaVersion = "1.0"

type PackageMode string

const (
	PackageDevelopment PackageMode = "development"
	PackageComplete    PackageMode = "complete"
	PackageLightweight PackageMode = "lightweight"
)

// These values are deliberately variables so release builds can set them with
// -ldflags "-X github.com/zouyi/eco-guardian/internal/buildinfo.<name>=...".
var (
	version     = "0.0.0-dev"
	build       = "development"
	commit      = "unknown"
	packageMode = string(PackageDevelopment)
)

// Info is safe to expose through runtime status. It intentionally contains no
// machine paths, secrets, or mutable settings.
type Info struct {
	Version                    string            `json:"version"`
	Build                      string            `json:"build"`
	Commit                     string            `json:"commit"`
	RuntimeStatusSchemaVersion string            `json:"runtime_status_schema_version"`
	PackageMode                PackageMode       `json:"package_mode"`
	EmbeddedAssetDigests       map[string]string `json:"embedded_asset_digests"`
}

var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// Current returns build identity with deterministic development defaults when
// no release linker values were supplied.
func Current() (Info, error) {
	if !semver.MatchString(version) {
		return Info{}, fmt.Errorf("invalid build version %q", version)
	}
	if strings.TrimSpace(build) == "" || strings.TrimSpace(commit) == "" {
		return Info{}, fmt.Errorf("build and commit identity are required")
	}
	mode := PackageMode(packageMode)
	if !mode.Valid() {
		return Info{}, fmt.Errorf("invalid package mode %q", packageMode)
	}
	digests, err := EmbeddedAssetDigests()
	if err != nil {
		return Info{}, err
	}
	return Info{
		Version:                    version,
		Build:                      build,
		Commit:                     commit,
		RuntimeStatusSchemaVersion: RuntimeStatusSchemaVersion,
		PackageMode:                mode,
		EmbeddedAssetDigests:       digests,
	}, nil
}

func (m PackageMode) Valid() bool {
	return m == PackageDevelopment || m == PackageComplete || m == PackageLightweight
}

// EmbeddedAssetDigests returns one SHA-256 digest for every file embedded in
// the executable. Map keys are slash-separated embedded paths.
func EmbeddedAssetDigests() (map[string]string, error) {
	digests, err := FileDigests(ecoguardian.Assets)
	if err != nil {
		return nil, err
	}
	grammar := sha256.Sum256([]byte(formula.DSLGrammar))
	digests["compiled/templates/dsl/dsl-v1.ebnf"] = hex.EncodeToString(grammar[:])
	for _, template := range scenario.BuiltinTemplates() {
		digests["compiled/templates/scenario/"+template.Definition.ID+"@"+template.Definition.Version+".json"] = template.BodyHash
	}
	starter := riskthreshold.StarterFixtureV1()
	digests["compiled/templates/risk/"+starter.ID+".json"] = starter.BodyHash
	return digests, nil
}

// FileDigests computes stable identities for a supplied asset filesystem. It
// is also used by packaged startup to compare the serving filesystem with the
// exact files compiled into the executable.
func FileDigests(source fs.FS) (map[string]string, error) {
	if source == nil {
		return nil, errors.New("embedded asset filesystem is unavailable")
	}
	var names []string
	if err := fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			names = append(names, name)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk embedded assets: %w", err)
	}
	sort.Strings(names)
	digests := make(map[string]string, len(names))
	for _, name := range names {
		contents, err := fs.ReadFile(source, name)
		if err != nil {
			return nil, fmt.Errorf("read embedded asset %q: %w", name, err)
		}
		sum := sha256.Sum256(contents)
		digests[name] = hex.EncodeToString(sum[:])
	}
	return digests, nil
}
