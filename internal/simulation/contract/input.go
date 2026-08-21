package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

const (
	SimulationInputSchemaV1 = "v1"
	DefaultSampleCount      = 1000
)

var ErrInputInvalid = errors.New("simulation input is invalid")

type MetricIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type InputRequest struct {
	Revision    Revision
	Scene       scenario.Template
	SampleCount int
	Seed        *uint64
	Metrics     []MetricIdentity
}

// SimulationInputV1 contains only immutable, fully expanded semantic facts.
// UI fields, Job IDs, request time, workers, and mutable source pointers are
// intentionally absent.
type SimulationInputV1 struct {
	SchemaVersion string                 `json:"schema_version"`
	ProjectID     ID                     `json:"project_id"`
	RevisionID    ID                     `json:"revision_id"`
	ConfigHash    string                 `json:"config_hash"`
	ManifestHash  string                 `json:"version_manifest_hash"`
	SceneID       string                 `json:"scene_id"`
	SceneVersion  string                 `json:"scene_version"`
	SceneBodyHash string                 `json:"scene_body_hash"`
	Participants  []scenario.Participant `json:"participants"`
	Actions       []scenario.Action      `json:"actions"`
	DurationMS    int64                  `json:"duration_ms"`
	Budgets       scenario.Budgets       `json:"budgets"`
	SampleCount   int                    `json:"sample_count"`
	Seed          uint64                 `json:"seed"`
	Metrics       []MetricIdentity       `json:"metrics"`
}

func NormalizeInput(request InputRequest) (SimulationInputV1, error) {
	if request.Revision.ID == "" || request.Revision.ProjectID == "" || strings.TrimSpace(request.Revision.ConfigHash) == "" || strings.TrimSpace(request.Revision.ManifestHash) == "" || len(request.Metrics) == 0 {
		return SimulationInputV1{}, ErrInputInvalid
	}
	definition, err := scenario.ParseDefinition(request.Scene.Body)
	if err != nil {
		return SimulationInputV1{}, fmt.Errorf("%w: %v", ErrInputInvalid, err)
	}
	body, err := json.Marshal(definition)
	if err != nil || string(body) != string(request.Scene.Body) {
		return SimulationInputV1{}, fmt.Errorf("%w: noncanonical scene body", ErrInputInvalid)
	}
	digest := sha256.Sum256(body)
	if request.Scene.BodyHash != hex.EncodeToString(digest[:]) || request.Scene.Definition.ID != definition.ID || request.Scene.Definition.Version != definition.Version {
		return SimulationInputV1{}, fmt.Errorf("%w: scene identity mismatch", ErrInputInvalid)
	}
	sampleCount := request.SampleCount
	if sampleCount == 0 {
		sampleCount = DefaultSampleCount
	}
	if sampleCount < 1 || sampleCount > definition.Budgets.MaxSamples {
		return SimulationInputV1{}, fmt.Errorf("%w: sample count", ErrInputInvalid)
	}
	seed := definition.DefaultSeed
	if request.Seed != nil {
		seed = *request.Seed
	}
	metrics, err := normalizeMetrics(request.Metrics)
	if err != nil {
		return SimulationInputV1{}, err
	}
	return SimulationInputV1{SchemaVersion: SimulationInputSchemaV1, ProjectID: request.Revision.ProjectID, RevisionID: request.Revision.ID, ConfigHash: request.Revision.ConfigHash, ManifestHash: request.Revision.ManifestHash, SceneID: definition.ID, SceneVersion: definition.Version, SceneBodyHash: request.Scene.BodyHash, Participants: append([]scenario.Participant(nil), definition.Participants...), Actions: append([]scenario.Action(nil), definition.Actions...), DurationMS: definition.DurationMS, Budgets: definition.Budgets, SampleCount: sampleCount, Seed: seed, Metrics: metrics}, nil
}

func normalizeMetrics(metrics []MetricIdentity) ([]MetricIdentity, error) {
	result := append([]MetricIdentity(nil), metrics...)
	for _, metric := range result {
		if strings.TrimSpace(metric.ID) == "" || strings.TrimSpace(metric.Version) == "" {
			return nil, ErrInputInvalid
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	for index := 1; index < len(result); index++ {
		if result[index-1].ID == result[index].ID {
			return nil, fmt.Errorf("%w: duplicate metric", ErrInputInvalid)
		}
	}
	return result, nil
}
