package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const fingerprintDomainSeparatorV1 = "eco-guardian/simulation-fingerprint/v1\x00"

// RevisionImplementation is the transport-neutral projection of a pinned
// revision VersionManifest entry. The composition adapter must preserve the
// recorded state rather than substituting a current implementation.
type RevisionImplementation struct {
	CapabilityID          string `json:"capability_id"`
	ContractVersion       string `json:"contract_version"`
	ImplementationVersion string `json:"implementation_version"`
	State                 string `json:"state"`
}

type ImplementationFingerprint struct {
	RevisionManifestHash string                   `json:"revision_manifest_hash"`
	SceneBodyHash        string                   `json:"scene_body_hash"`
	Revision             []RevisionImplementation `json:"revision"`
	Simulation           []Descriptor             `json:"simulation"`
}

var requiredRevisionCapabilities = []string{"schema", "dsl", "validator-registry", "numeric-policy"}

func ResolveFingerprint(registry *ManifestRegistry, input SimulationInputV1, revision []RevisionImplementation) (ImplementationFingerprint, string, error) {
	if registry == nil || input.SceneBodyHash == "" || input.ManifestHash == "" {
		return ImplementationFingerprint{}, "", ErrInputInvalid
	}
	resolvedRevision, err := normalizeRevisionImplementations(revision)
	if err != nil {
		return ImplementationFingerprint{}, "", err
	}
	for _, capability := range requiredRevisionCapabilities {
		found := false
		for _, implementation := range resolvedRevision {
			if implementation.CapabilityID == capability && implementation.State == "registered" {
				found = true
				break
			}
		}
		if !found {
			return ImplementationFingerprint{}, "", fmt.Errorf("%w: missing pinned revision capability %q", ErrInputInvalid, capability)
		}
	}
	components := registry.Descriptors()
	if len(components) != len(RequiredV1Descriptors) {
		return ImplementationFingerprint{}, "", fmt.Errorf("%w: incomplete simulation registry", ErrInputInvalid)
	}
	fingerprint := ImplementationFingerprint{RevisionManifestHash: input.ManifestHash, SceneBodyHash: input.SceneBodyHash, Revision: resolvedRevision, Simulation: components}
	body, err := json.Marshal(fingerprint)
	if err != nil {
		return ImplementationFingerprint{}, "", err
	}
	digest := sha256.Sum256(append([]byte(fingerprintDomainSeparatorV1), body...))
	return fingerprint, hex.EncodeToString(digest[:]), nil
}

func normalizeRevisionImplementations(entries []RevisionImplementation) ([]RevisionImplementation, error) {
	if len(entries) == 0 {
		return nil, ErrInputInvalid
	}
	result := append([]RevisionImplementation(nil), entries...)
	for _, entry := range result {
		if strings.TrimSpace(entry.CapabilityID) == "" || strings.TrimSpace(entry.ContractVersion) == "" || (entry.State == "registered" && strings.TrimSpace(entry.ImplementationVersion) == "") || (entry.State != "registered" && entry.State != "unregistered") {
			return nil, ErrInputInvalid
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CapabilityID < result[j].CapabilityID })
	for index := 1; index < len(result); index++ {
		if result[index-1].CapabilityID == result[index].CapabilityID {
			return nil, fmt.Errorf("%w: duplicate revision capability", ErrInputInvalid)
		}
	}
	return result, nil
}
