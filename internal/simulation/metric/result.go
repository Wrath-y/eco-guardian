package metric

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

const resultDomainSeparatorV1 = "eco-guardian/simulation-result/v1\x00"

var ErrResultInvalid = errors.New("simulation result is invalid")

type CanonicalValueRange struct {
	Lower  string      `json:"lower"`
	Upper  string      `json:"upper"`
	Bounds RangeBounds `json:"bounds"`
}

type CanonicalMetric struct {
	ID                string               `json:"id"`
	Version           string               `json:"version"`
	Status            Status               `json:"status"`
	Unit              string               `json:"unit"`
	Direction         Direction            `json:"direction"`
	TargetRange       *CanonicalValueRange `json:"target_range"`
	AbsoluteThreshold *string              `json:"absolute_threshold"`
	Value             string               `json:"value,omitempty"`
	ConfidenceLow     string               `json:"confidence_low,omitempty"`
	ConfidenceHigh    string               `json:"confidence_high,omitempty"`
	SampleCount       int                  `json:"sample_count"`
	Assumptions       []string             `json:"assumptions"`
	Unavailable       *UnavailableReason   `json:"unavailable,omitempty"`
}

type CanonicalResultV1 struct {
	SchemaVersion   string            `json:"schema_version"`
	InputHash       string            `json:"input_hash"`
	FingerprintHash string            `json:"fingerprint_hash"`
	Metrics         []CanonicalMetric `json:"metrics"`
	Warnings        []string          `json:"warnings"`
}

func NewCanonicalResult(inputHash, fingerprintHash string, aggregates []AggregateResult, warnings []string) (CanonicalResultV1, error) {
	if len(inputHash) != 64 || len(fingerprintHash) != 64 || len(aggregates) == 0 {
		return CanonicalResultV1{}, ErrResultInvalid
	}
	metrics := make([]CanonicalMetric, 0, len(aggregates))
	for _, aggregate := range aggregates {
		if !aggregate.Valid() {
			return CanonicalResultV1{}, ErrResultInvalid
		}
		metric := CanonicalMetric{ID: aggregate.Descriptor.ID, Version: aggregate.Descriptor.Version, Status: aggregate.Status, Unit: aggregate.Descriptor.Unit, Direction: aggregate.Descriptor.Direction, SampleCount: aggregate.SampleCount, Assumptions: append([]string(nil), aggregate.Descriptor.Assumptions...)}
		if aggregate.Descriptor.TargetRange != nil {
			metric.TargetRange = &CanonicalValueRange{Lower: aggregate.Descriptor.TargetRange.Lower.String(), Upper: aggregate.Descriptor.TargetRange.Upper.String(), Bounds: aggregate.Descriptor.TargetRange.Bounds}
		}
		if aggregate.Descriptor.AbsoluteThreshold != nil {
			value := aggregate.Descriptor.AbsoluteThreshold.String()
			metric.AbsoluteThreshold = &value
		}
		if aggregate.Status == Available {
			metric.Value, metric.ConfidenceLow, metric.ConfidenceHigh = aggregate.Value.String(), aggregate.ConfidenceLow.String(), aggregate.ConfidenceHigh.String()
		} else {
			unavailable := *aggregate.Unavailable
			unavailable.Missing = append([]string(nil), unavailable.Missing...)
			metric.Unavailable = &unavailable
		}
		metrics = append(metrics, metric)
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].ID < metrics[j].ID })
	for index := 1; index < len(metrics); index++ {
		if metrics[index-1].ID == metrics[index].ID {
			return CanonicalResultV1{}, ErrResultInvalid
		}
	}
	canonicalWarnings := append([]string(nil), warnings...)
	sort.Strings(canonicalWarnings)
	return CanonicalResultV1{SchemaVersion: "v1", InputHash: inputHash, FingerprintHash: fingerprintHash, Metrics: metrics, Warnings: canonicalWarnings}, nil
}

func (result CanonicalResultV1) Bytes() ([]byte, error) {
	if result.SchemaVersion != "v1" || len(result.InputHash) != 64 || len(result.FingerprintHash) != 64 || len(result.Metrics) == 0 {
		return nil, ErrResultInvalid
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return append([]byte(resultDomainSeparatorV1), body...), nil
}
func (result CanonicalResultV1) Hash() (string, error) {
	body, err := result.Bytes()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}
