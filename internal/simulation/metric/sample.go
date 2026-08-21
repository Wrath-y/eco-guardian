package metric

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

const sampleDomainSeparatorV1 = "eco-guardian/simulation-sample/v1\x00"

var ErrSampleInvalid = errors.New("simulation metric sample is invalid")

// SampleHash is the stable identity of a completed sample's structured
// observations. Observation arrival order is transport detail, not semantics.
func SampleHash(sample Sample) (string, error) {
	if sample.Status != "" && sample.Status != SampleSucceeded || len(sample.Observations) == 0 {
		return "", ErrSampleInvalid
	}
	type observation struct {
		ID    string `json:"id"`
		Unit  string `json:"unit"`
		Value string `json:"value"`
	}
	observations := make([]observation, 0, len(sample.Observations))
	for _, item := range sample.Observations {
		if item.ID == "" || item.Unit == "" {
			return "", ErrSampleInvalid
		}
		observations = append(observations, observation{ID: item.ID, Unit: item.Unit, Value: item.Value.String()})
	}
	sort.Slice(observations, func(i, j int) bool {
		if observations[i].ID != observations[j].ID {
			return observations[i].ID < observations[j].ID
		}
		if observations[i].Unit != observations[j].Unit {
			return observations[i].Unit < observations[j].Unit
		}
		return observations[i].Value < observations[j].Value
	})
	body, err := json.Marshal(struct {
		Ordinal      uint64        `json:"ordinal"`
		Observations []observation `json:"observations"`
	}{Ordinal: sample.Ordinal, Observations: observations})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(append([]byte(sampleDomainSeparatorV1), body...))
	return hex.EncodeToString(digest[:]), nil
}
