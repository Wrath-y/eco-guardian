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
	Revision                Revision
	RuleMaterializationHash string
	Scene                   scenario.Template
	SampleCount             int
	Seed                    *uint64
	Budget                  *BudgetOverride
	Metrics                 []MetricIdentity
}

// BudgetOverride can only tighten the versioned scenario limits. Nil leaves
// the captured scene budget intact; zero is never a sentinel value.
type BudgetOverride struct {
	MaxEvents    *int
	MaxSteps     *int
	MaxRuntimeMS *int
}

// SimulationInputV1 contains only immutable, fully expanded semantic facts.
// UI fields, Job IDs, request time, workers, and mutable source pointers are
// intentionally absent.
type SimulationInputV1 struct {
	SchemaVersion           string                 `json:"schema_version"`
	ProjectID               ID                     `json:"project_id"`
	RevisionID              ID                     `json:"revision_id"`
	ConfigHash              string                 `json:"config_hash"`
	ManifestHash            string                 `json:"version_manifest_hash"`
	RuleMaterializationHash string                 `json:"rule_materialization_hash,omitempty"`
	SceneID                 string                 `json:"scene_id"`
	SceneVersion            string                 `json:"scene_version"`
	SceneBodyHash           string                 `json:"scene_body_hash"`
	Participants            []scenario.Participant `json:"participants"`
	Actions                 []scenario.Action      `json:"actions"`
	DurationMS              int64                  `json:"duration_ms"`
	Budgets                 scenario.Budgets       `json:"budgets"`
	SampleCount             int                    `json:"sample_count"`
	Seed                    uint64                 `json:"seed"`
	Metrics                 []MetricIdentity       `json:"metrics"`
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
	budgets, err := normalizeBudgets(definition.Budgets, request.Budget)
	if err != nil {
		return SimulationInputV1{}, err
	}
	if request.RuleMaterializationHash != "" && (len(request.RuleMaterializationHash) != 64 || !isLowerHex(request.RuleMaterializationHash)) {
		return SimulationInputV1{}, fmt.Errorf("%w: rule materialization hash", ErrInputInvalid)
	}
	return SimulationInputV1{SchemaVersion: SimulationInputSchemaV1, ProjectID: request.Revision.ProjectID, RevisionID: request.Revision.ID, ConfigHash: request.Revision.ConfigHash, ManifestHash: request.Revision.ManifestHash, RuleMaterializationHash: request.RuleMaterializationHash, SceneID: definition.ID, SceneVersion: definition.Version, SceneBodyHash: request.Scene.BodyHash, Participants: append([]scenario.Participant(nil), definition.Participants...), Actions: append([]scenario.Action(nil), definition.Actions...), DurationMS: definition.DurationMS, Budgets: budgets, SampleCount: sampleCount, Seed: seed, Metrics: metrics}, nil
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func normalizeBudgets(base scenario.Budgets, override *BudgetOverride) (scenario.Budgets, error) {
	if override == nil {
		return base, nil
	}
	result := base
	for _, value := range []struct {
		value *int
		limit *int
	}{{override.MaxEvents, &result.MaxEvents}, {override.MaxSteps, &result.MaxSteps}, {override.MaxRuntimeMS, &result.MaxRuntimeMS}} {
		if value.value == nil {
			continue
		}
		if *value.value < 1 || *value.value > *value.limit {
			return scenario.Budgets{}, fmt.Errorf("%w: budget", ErrInputInvalid)
		}
		*value.limit = *value.value
	}
	return result, nil
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
