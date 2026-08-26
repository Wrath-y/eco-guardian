package packageinfo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const RuntimeContractPinsPath = "packaging/runtime-contract-pins.json"

type RuntimeContractPins struct {
	SchemaVersion    int                  `json:"schema_version"`
	LocalRAG         LocalRAGPin          `json:"local_rag"`
	OpenAPI          ContractFilePin      `json:"openapi"`
	ConsumerFixtures []ConsumerFixturePin `json:"consumer_fixtures"`
}

type LocalRAGPin struct {
	Version      string `json:"version"`
	SourceCommit string `json:"source_commit"`
	HealthPath   string `json:"health_path"`
	HealthSHA256 string `json:"health_sha256"`
}

type ContractFilePin struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

type ConsumerFixturePin struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

// LoadAndVerifyRuntimeContractPins validates the source-controlled compatibility
// boundary without exposing repository paths or contract contents in errors.
func LoadAndVerifyRuntimeContractPins(repositoryRoot, pinsPath string) (RuntimeContractPins, error) {
	var pins RuntimeContractPins
	data, err := readPinnedRepositoryFile(repositoryRoot, pinsPath)
	if err != nil {
		return pins, diagnostic(CodePinSetCorrupt, "")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&pins); err != nil || decoder.Decode(&struct{}{}) != io.EOF || pins.validateShape() != nil {
		return RuntimeContractPins{}, diagnostic(CodePinSetCorrupt, "")
	}
	if err = verifyPinnedFile(repositoryRoot, pins.OpenAPI.Path, pins.OpenAPI.SHA256); err != nil {
		return RuntimeContractPins{}, diagnostic(CodeOpenAPIDrift, "")
	}
	if openAPIVersion(repositoryRoot, pins.OpenAPI.Path) != pins.OpenAPI.Version {
		return RuntimeContractPins{}, diagnostic(CodeOpenAPIDrift, "")
	}
	if err = verifyPinnedFile(repositoryRoot, pins.LocalRAG.HealthPath, pins.LocalRAG.HealthSHA256); err != nil || !healthVersionsMatch(repositoryRoot, pins.LocalRAG) {
		return RuntimeContractPins{}, diagnostic(CodeLocalRAGVersionDrift, ComponentLocalRAG)
	}
	for _, fixture := range pins.ConsumerFixtures {
		if !verifyConsumerFixture(repositoryRoot, fixture, pins) {
			return RuntimeContractPins{}, diagnostic(CodeConsumerFixtureDrift, "")
		}
	}
	return pins, nil
}

func (pins RuntimeContractPins) validateShape() error {
	if pins.SchemaVersion != 1 || !pinnedVersion(pins.LocalRAG.Version) || len(pins.LocalRAG.SourceCommit) != 40 || !isLowerHex(pins.LocalRAG.SourceCommit) || !validContractPin(pins.LocalRAG.HealthPath, pins.LocalRAG.HealthSHA256) {
		return errors.New("invalid pins")
	}
	if !semverPattern.MatchString(pins.OpenAPI.Version) || !validContractPin(pins.OpenAPI.Path, pins.OpenAPI.SHA256) || len(pins.ConsumerFixtures) == 0 {
		return errors.New("invalid pins")
	}
	seen := map[string]bool{}
	ordered := make([]string, 0, len(pins.ConsumerFixtures))
	for _, fixture := range pins.ConsumerFixtures {
		if !tokenPattern.MatchString(fixture.ID) || seen[fixture.ID] || strings.TrimSpace(fixture.Version) == "" || !validContractPin(fixture.Path, fixture.SHA256) {
			return errors.New("invalid pins")
		}
		seen[fixture.ID] = true
		ordered = append(ordered, fixture.ID)
	}
	if !sort.StringsAreSorted(ordered) {
		return errors.New("invalid pins")
	}
	return nil
}

func validContractPin(relative, digest string) bool {
	return safeRelativePath(filepath.ToSlash(relative)) && hashPattern.MatchString(digest)
}

func VerifyPinnedLocalRAGVersion(pins RuntimeContractPins, version string) error {
	if version != pins.LocalRAG.Version {
		return diagnostic(CodeLocalRAGVersionDrift, ComponentLocalRAG)
	}
	return nil
}

func verifyPinnedFile(root, relative, want string) error {
	body, err := readPinnedRepositoryFile(root, relative)
	if err != nil || digestBytes(body) != want {
		return errors.New("contract drift")
	}
	return nil
}

func readPinnedRepositoryFile(root, relative string) ([]byte, error) {
	if strings.TrimSpace(root) == "" || !safeRelativePath(filepath.ToSlash(relative)) {
		return nil, errors.New("unsafe pin path")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	filename := filepath.Join(absoluteRoot, filepath.FromSlash(relative))
	info, err := os.Lstat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("pin file unavailable")
	}
	return os.ReadFile(filename)
}

func fixtureManifestVersion(body []byte) string {
	var value struct {
		FixtureVersion string `json:"fixture_version"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&value); err != nil {
		return ""
	}
	return value.FixtureVersion
}

func verifyConsumerFixture(root string, pin ConsumerFixturePin, pins RuntimeContractPins) bool {
	body, err := readPinnedRepositoryFile(root, pin.Path)
	if err != nil || digestBytes(body) != pin.SHA256 || fixtureManifestVersion(body) != pin.Version {
		return false
	}
	var manifest struct {
		FixtureVersion string            `json:"fixture_version"`
		Algorithm      string            `json:"algorithm"`
		Files          map[string]string `json:"files"`
	}
	if json.Unmarshal(body, &manifest) != nil || len(manifest.Files) == 0 || manifest.Algorithm != "" && manifest.Algorithm != "sha256" {
		return false
	}
	base := filepath.ToSlash(filepath.Dir(pin.Path))
	for name, want := range manifest.Files {
		if !safeRelativePath(name) || !hashPattern.MatchString(want) {
			return false
		}
		contents, readErr := readPinnedRepositoryFile(root, filepath.ToSlash(filepath.Join(base, name)))
		if readErr != nil || digestBytes(contents) != want {
			return false
		}
		if name == "consumer-contract.json" {
			var contract struct {
				SourceCommit  string `json:"source_commit"`
				OpenAPISHA256 string `json:"openapi_sha256"`
			}
			if json.Unmarshal(contents, &contract) != nil || contract.SourceCommit != pins.LocalRAG.SourceCommit || contract.OpenAPISHA256 != pins.OpenAPI.SHA256 {
				return false
			}
		}
	}
	return true
}

func openAPIVersion(root, relative string) string {
	body, err := readPinnedRepositoryFile(root, relative)
	if err != nil {
		return ""
	}
	var document struct {
		Info struct {
			Version string `yaml:"version"`
		} `yaml:"info"`
	}
	if yaml.Unmarshal(body, &document) != nil {
		return ""
	}
	return document.Info.Version
}

func healthVersionsMatch(root string, pin LocalRAGPin) bool {
	body, err := readPinnedRepositoryFile(root, pin.HealthPath)
	if err != nil {
		return false
	}
	var observations map[string]struct {
		Service        string `json:"service"`
		ServiceVersion string `json:"service_version"`
	}
	if json.Unmarshal(body, &observations) != nil || len(observations) == 0 {
		return false
	}
	for _, observation := range observations {
		if observation.Service != "local-rag" || observation.ServiceVersion != pin.Version {
			return false
		}
	}
	return true
}

func digestBytes(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func isLowerHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}
