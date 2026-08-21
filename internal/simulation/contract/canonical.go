package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const inputDomainSeparatorV1 = "eco-guardian/simulation-input/v1\x00"

// CanonicalBytes serializes a fully normalized input. SimulationInputV1 has no
// maps or floats, while its Metric set is normalized before this boundary;
// semantic sequences such as actions retain their declared order.
func (input SimulationInputV1) CanonicalBytes() ([]byte, error) {
	if input.SchemaVersion != SimulationInputSchemaV1 || input.ProjectID == "" || input.RevisionID == "" || input.SceneID == "" || input.SceneVersion == "" || input.SampleCount < 1 || len(input.Metrics) == 0 {
		return nil, ErrInputInvalid
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInputInvalid, err)
	}
	return append([]byte(inputDomainSeparatorV1), body...), nil
}

func (input SimulationInputV1) Hash() (string, error) {
	body, err := input.CanonicalBytes()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}
