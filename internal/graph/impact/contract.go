package impact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

var ErrorCodes = []string{
	"GRAPH_IDENTITY_MISMATCH", "GRAPH_NOT_READY", "GRAPH_STORE_UNAVAILABLE",
	"IDEMPOTENCY_CONFLICT", "INTERNAL_ERROR", "INVALID_GRAPH_QUERY",
	"INVALID_IMPACT_INPUT", "INVALID_REVISION_PAIR", "LIMIT_EXCEEDED",
	"NODE_NOT_FOUND", "NO_BASELINE", "PROVIDER_CONTRACT_MISMATCH",
	"RETRIEVAL_UNAVAILABLE", "SNAPSHOT_INDEX_NOT_READY", "VALIDATION_REQUIRED",
}

type ContractManifest struct {
	AnalysisContractVersion string   `json:"analysis_contract_version"`
	Directions              []string `json:"directions"`
	RelationshipKinds       []string `json:"relationship_kinds"`
	TruncationReasons       []string `json:"truncation_reasons"`
	ErrorCodes              []string `json:"error_codes"`
}

func Manifest() ContractManifest {
	errors := append([]string(nil), ErrorCodes...)
	sort.Strings(errors)
	return ContractManifest{
		AnalysisContractVersion: AnalysisContractVersion,
		Directions:              []string{string(DirectionBoth), string(DirectionIncoming), string(DirectionOutgoing)},
		RelationshipKinds:       []string{"explicit"},
		TruncationReasons:       []string{string(MaxDepth), string(MaxNodes), string(MaxPaths)},
		ErrorCodes:              errors,
	}
}

func ValidateManifest(manifest ContractManifest) error {
	if manifest.AnalysisContractVersion != AnalysisContractVersion {
		return fmt.Errorf("analysis contract drift")
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	canonical, err := json.Marshal(Manifest())
	if err != nil || string(encoded) != string(canonical) {
		return fmt.Errorf("impact identity manifest drift")
	}
	for name, values := range map[string][]string{"directions": manifest.Directions, "relationship kinds": manifest.RelationshipKinds, "truncation reasons": manifest.TruncationReasons, "error codes": manifest.ErrorCodes} {
		seen := map[string]struct{}{}
		for _, value := range values {
			if value == "" {
				return fmt.Errorf("empty %s identity", name)
			}
			if _, duplicate := seen[value]; duplicate {
				return fmt.Errorf("duplicate %s identity %s", name, value)
			}
			seen[value] = struct{}{}
		}
	}
	return nil
}

func EvidenceID(kind string, canonical any) (string, error) {
	encoded, err := json.Marshal(struct {
		Version string `json:"version"`
		Kind    string `json:"kind"`
		Value   any    `json:"value"`
	}{AnalysisContractVersion, kind, canonical})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
