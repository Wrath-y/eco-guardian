package threshold

import (
	riskcontract "github.com/zouyi/eco-guardian/internal/risk/contract"
)

const StarterIDV1 = "risk-threshold-starter-v1"

type StarterTemplate struct {
	ID       string `json:"id"`
	Enabled  bool   `json:"enabled"`
	Body     Body   `json:"body"`
	BodyHash string `json:"body_hash"`
}

func (s StarterTemplate) Valid() bool {
	if s.ID != StarterIDV1 || s.Enabled || !s.Body.Valid() || !validHash(s.BodyHash) {
		return false
	}
	hash, err := s.Body.Hash()
	return err == nil && hash == s.BodyHash
}

func StarterFixtureV1() StarterTemplate {
	type metricDefinition struct {
		id, unit  string
		direction riskcontract.MetricDirection
	}
	scenes := []string{"single-target-30s", "single-target-180s", "three-target-60s", "extreme-stacking-60s"}
	metrics := []metricDefinition{
		{"metric-dps", "points_per_second", riskcontract.HigherIsRisk},
		{"metric-healing", "points_per_second", riskcontract.HigherIsRisk},
		{"metric-survivability", "milliseconds", riskcontract.LowerIsRisk},
		{"metric-resource", "ratio", riskcontract.TargetRange},
		{"metric-control", "milliseconds", riskcontract.HigherIsRisk},
	}
	entries := make([]Entry, 0, len(scenes)*len(metrics))
	for _, scene := range scenes {
		for _, metric := range metrics {
			entries = append(entries, Entry{SceneID: scene, SceneVersion: "v1", MetricID: metric.id, MetricVersion: "v1", Unit: metric.unit, Direction: metric.direction, Relative: Boundaries{Warning: "0.10", Block: "0.25"}})
		}
	}
	structuralHash := ""
	for _, manifest := range riskcontract.V1Manifests() {
		if manifest.Kind == riskcontract.StructuralManifest && manifest.ID == "risk-structural" {
			structuralHash = manifest.SourceHash
			break
		}
	}
	body := Body{SchemaVersion: SchemaVersionV1, Source: "Eco Guardian inactive starter threshold v1; explicit project activation required", Assumptions: []string{"relative risk boundaries only", "absolute boundaries require an explicit compatible Metric threshold entry", "starter activation is never implicit"}, Entries: entries, StructuralRuleVersions: []riskcontract.Identity{{ID: "risk-structural", Version: "v1", Hash: structuralHash}}}
	normalized, err := body.Normalize()
	if err != nil {
		panic("invalid built-in risk threshold starter")
	}
	hash, err := normalized.Hash()
	if err != nil {
		panic("invalid built-in risk threshold starter hash")
	}
	return StarterTemplate{ID: StarterIDV1, Enabled: false, Body: normalized, BodyHash: hash}
}
