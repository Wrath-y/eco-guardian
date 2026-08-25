// Package preview exposes the pure simulation seam used to assess an
// isolated proposal before it becomes a configuration revision. It composes
// the same versioned scenario, engine-input, evaluator, fingerprint, and Metric
// contracts as formal simulation without importing Job or persistence APIs.
package preview

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"

	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/engine"
	"github.com/zouyi/eco-guardian/internal/simulation/metric"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

const (
	SchemaVersionV1             = "v1"
	materializationHashDomainV1 = "eco-guardian/proposal-materialization/v1\x00"
	evaluatorDescriptorID       = "simulation-evaluator-adapter"
)

var (
	ErrInvalid     = errors.New("simulation preview is invalid")
	ErrUnavailable = errors.New("simulation preview implementation is unavailable")
)

// SealedMaterializationV1 binds canonical proposal bytes to the immutable
// revision identities from which they were derived. The body is data only; it
// is never interpreted as a revision or persisted by this package.
type SealedMaterializationV1 struct {
	SchemaVersion   string            `json:"schema_version"`
	BaseRevision    contract.Revision `json:"base_revision"`
	Materialization []byte            `json:"materialization"`
	Hash            string            `json:"hash"`
}

// SealMaterialization creates the shared proposal identity consumed by the AI
// workflow and the simulation preview boundary.
func SealMaterialization(base contract.Revision, canonical []byte) (SealedMaterializationV1, error) {
	if !validRevision(base) || len(canonical) == 0 {
		return SealedMaterializationV1{}, ErrInvalid
	}
	digest := sha256.Sum256(append([]byte(materializationHashDomainV1), canonical...))
	return SealedMaterializationV1{
		SchemaVersion:   SchemaVersionV1,
		BaseRevision:    base,
		Materialization: append([]byte(nil), canonical...),
		Hash:            hex.EncodeToString(digest[:]),
	}, nil
}

func (materialization SealedMaterializationV1) valid() bool {
	if materialization.SchemaVersion != SchemaVersionV1 || !validRevision(materialization.BaseRevision) || len(materialization.Materialization) == 0 || !validHash(materialization.Hash) {
		return false
	}
	digest := sha256.Sum256(append([]byte(materializationHashDomainV1), materialization.Materialization...))
	return materialization.Hash == hex.EncodeToString(digest[:])
}

// Request contains only frozen semantic input. Job IDs, timestamps, worker
// counts, current settings, and storage handles cannot enter preview identity.
type Request struct {
	Materialization         SealedMaterializationV1
	SceneID                 string
	SceneVersion            string
	SampleCount             int
	Seed                    *uint64
	Budget                  *contract.BudgetOverride
	Metrics                 []contract.MetricIdentity
	RevisionImplementations []contract.RevisionImplementation
}

// SampleRequest is the immutable input to the already registered #10
// evaluator adapter. InitialEvents and Limits are prepared by the existing
// simulation engine contracts rather than recreated by an AI consumer.
type SampleRequest struct {
	Materialization SealedMaterializationV1
	Input           contract.SimulationInputV1
	Scene           scenario.Template
	InitialEvents   []engine.Event
	Limits          engine.Limits
	Ordinal         uint64
}

// SampleEvaluator is the versioned pure evaluator port. Implementations use
// the registered #10 engine/evaluator semantics and return structured Metric
// observations; they cannot receive a repository or formal run identity.
type SampleEvaluator interface {
	Descriptor() contract.Descriptor
	EvaluateSample(context.Context, SampleRequest) (metric.Sample, error)
}

// EventAdapter is the existing registered evaluator-specific seam: it prepares
// typed event evaluation and exposes structured observations after the engine
// finishes. It cannot replace engine ordering, budgets, or cancellation.
type EventAdapter interface {
	Descriptor() contract.Descriptor
	Prepare(context.Context, SampleRequest) (engine.Evaluator, ObservationReader, error)
}

type ObservationReader func(engine.Stats) ([]metric.Observation, error)

// EngineSampleEvaluator is the production pure adapter that runs the existing
// bounded event engine. Domain-specific evaluator versions only implement the
// typed EventAdapter; they do not implement scheduling or Metric reduction.
type EngineSampleEvaluator struct{ Adapter EventAdapter }

func (evaluator EngineSampleEvaluator) Descriptor() contract.Descriptor {
	if evaluator.Adapter == nil {
		return contract.Descriptor{}
	}
	return evaluator.Adapter.Descriptor()
}

func (evaluator EngineSampleEvaluator) EvaluateSample(ctx context.Context, request SampleRequest) (metric.Sample, error) {
	if evaluator.Adapter == nil || request.Ordinal >= uint64(request.Input.SampleCount) || !request.Limits.Valid() {
		return metric.Sample{}, ErrInvalid
	}
	queue := engine.NewQueue()
	for _, event := range request.InitialEvents {
		if err := queue.Enqueue(event); err != nil {
			return metric.Sample{}, errors.Join(ErrInvalid, err)
		}
	}
	evaluate, observations, err := evaluator.Adapter.Prepare(ctx, request)
	if err != nil {
		return metric.Sample{}, err
	}
	if evaluate == nil || observations == nil {
		return metric.Sample{}, ErrUnavailable
	}
	stats, err := engine.RunWithCheckpoint(queue, request.Limits, evaluate, func(engine.Stats) error { return ctx.Err() })
	if err != nil {
		return metric.Sample{}, err
	}
	values, err := observations(stats)
	if err != nil {
		return metric.Sample{}, err
	}
	return metric.Sample{Ordinal: request.Ordinal, Observations: append([]metric.Observation(nil), values...), Status: metric.SampleSucceeded}, nil
}

// ScenarioRegistry is satisfied by *scenario.Registry.
type ScenarioRegistry interface {
	Get(string, string) (scenario.Template, bool)
}

// ResultV1 is advisory evidence only. Its canonical result reuses #10 Metric
// result hashing, while the input hash includes the sealed materialization.
type ResultV1 struct {
	SchemaVersion       string                             `json:"schema_version"`
	Advisory            bool                               `json:"advisory"`
	MaterializationHash string                             `json:"materialization_hash"`
	Input               contract.SimulationInputV1         `json:"input"`
	InputHash           string                             `json:"input_hash"`
	Fingerprint         contract.ImplementationFingerprint `json:"fingerprint"`
	FingerprintHash     string                             `json:"fingerprint_hash"`
	Result              metric.CanonicalResultV1           `json:"result"`
	ResultHash          string                             `json:"result_hash"`
}

// Evaluator is the transport-neutral proposal preview port consumed by #12.
type Evaluator interface {
	Evaluate(context.Context, Request) (ResultV1, error)
}

// Service composes existing pure #10 contracts. It has deliberately no clock,
// ID generator, Job store, run repository, or report-sealing dependency.
type Service struct {
	Scenarios ScenarioRegistry
	Metrics   *metric.Registry
	Manifests *contract.ManifestRegistry
	Samples   SampleEvaluator
}

func (service Service) Evaluate(ctx context.Context, request Request) (ResultV1, error) {
	if service.Scenarios == nil || service.Metrics == nil || service.Manifests == nil || service.Samples == nil || !request.Materialization.valid() || strings.TrimSpace(request.SceneID) == "" || strings.TrimSpace(request.SceneVersion) == "" {
		return ResultV1{}, ErrInvalid
	}
	template, found := service.Scenarios.Get(request.SceneID, request.SceneVersion)
	if !found {
		return ResultV1{}, ErrUnavailable
	}
	input, err := contract.NormalizeInput(contract.InputRequest{
		Revision:                request.Materialization.BaseRevision,
		RuleMaterializationHash: request.Materialization.Hash,
		Scene:                   template,
		SampleCount:             request.SampleCount,
		Seed:                    cloneSeed(request.Seed),
		Budget:                  cloneBudget(request.Budget),
		Metrics:                 append([]contract.MetricIdentity(nil), request.Metrics...),
	})
	if err != nil {
		return ResultV1{}, errors.Join(ErrInvalid, err)
	}
	selectedMetrics, err := selectMetrics(service.Metrics, input.Metrics)
	if err != nil {
		return ResultV1{}, err
	}
	registeredEvaluator, found := service.Manifests.Descriptor(evaluatorDescriptorID)
	if !found || !sameDescriptor(registeredEvaluator, service.Samples.Descriptor()) {
		return ResultV1{}, ErrUnavailable
	}
	fingerprint, fingerprintHash, err := contract.ResolveFingerprint(service.Manifests, input, request.RevisionImplementations)
	if err != nil {
		return ResultV1{}, errors.Join(ErrUnavailable, err)
	}
	inputHash, err := input.Hash()
	if err != nil {
		return ResultV1{}, errors.Join(ErrInvalid, err)
	}
	initialEvents, err := engine.InitialEvents(template.Definition)
	if err != nil {
		return ResultV1{}, errors.Join(ErrInvalid, err)
	}
	samples := make([]metric.Sample, input.SampleCount)
	for ordinal := range samples {
		if err = ctx.Err(); err != nil {
			return ResultV1{}, err
		}
		sample, sampleErr := service.Samples.EvaluateSample(ctx, SampleRequest{
			Materialization: cloneMaterialization(request.Materialization),
			Input:           cloneInput(input),
			Scene:           cloneTemplate(template),
			InitialEvents:   cloneEvents(initialEvents),
			Limits:          engine.Limits{DurationMS: input.DurationMS, MaxEvents: input.Budgets.MaxEvents, MaxSteps: input.Budgets.MaxSteps},
			Ordinal:         uint64(ordinal),
		})
		if sampleErr != nil {
			return ResultV1{}, sampleErr
		}
		if sample.Ordinal != uint64(ordinal) || sample.Status != metric.SampleSucceeded {
			return ResultV1{}, ErrInvalid
		}
		samples[ordinal] = cloneSample(sample)
	}
	aggregates, err := metric.ReduceExpectedWithCheck(selectedMetrics, samples, input.SampleCount, ctx.Err)
	if err != nil {
		return ResultV1{}, err
	}
	canonical, err := metric.NewCanonicalResult(inputHash, fingerprintHash, aggregates, nil)
	if err != nil {
		return ResultV1{}, err
	}
	resultHash, err := canonical.Hash()
	if err != nil {
		return ResultV1{}, err
	}
	return ResultV1{
		SchemaVersion:       SchemaVersionV1,
		Advisory:            true,
		MaterializationHash: request.Materialization.Hash,
		Input:               input,
		InputHash:           inputHash,
		Fingerprint:         fingerprint,
		FingerprintHash:     fingerprintHash,
		Result:              canonical,
		ResultHash:          resultHash,
	}, nil
}

func selectMetrics(registry *metric.Registry, identities []contract.MetricIdentity) (*metric.Registry, error) {
	modules := make([]metric.Module, 0, len(identities))
	for _, identity := range identities {
		module, found := registry.Module(identity.ID)
		if !found || module.Descriptor().Version != identity.Version {
			return nil, ErrUnavailable
		}
		modules = append(modules, module)
	}
	selected, err := metric.NewRegistry(modules)
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	return selected, nil
}

func validRevision(revision contract.Revision) bool {
	return revision.ID != "" && revision.ProjectID != "" && validHash(revision.ConfigHash) && validHash(revision.ManifestHash)
}

func validHash(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sameDescriptor(left, right contract.Descriptor) bool {
	return left.ID == right.ID && left.Version == right.Version && left.Hash == right.Hash && reflect.DeepEqual(left.Dependencies, right.Dependencies)
}

func cloneSeed(seed *uint64) *uint64 {
	if seed == nil {
		return nil
	}
	value := *seed
	return &value
}

func cloneBudget(budget *contract.BudgetOverride) *contract.BudgetOverride {
	if budget == nil {
		return nil
	}
	copy := *budget
	copy.MaxEvents = cloneInt(budget.MaxEvents)
	copy.MaxSteps = cloneInt(budget.MaxSteps)
	copy.MaxRuntimeMS = cloneInt(budget.MaxRuntimeMS)
	return &copy
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneMaterialization(value SealedMaterializationV1) SealedMaterializationV1 {
	value.Materialization = append([]byte(nil), value.Materialization...)
	return value
}

func cloneInput(value contract.SimulationInputV1) contract.SimulationInputV1 {
	canonical, err := value.CanonicalBytes()
	if err != nil {
		return contract.SimulationInputV1{}
	}
	copy, err := contract.ParseCanonicalInput(canonical)
	if err != nil {
		return contract.SimulationInputV1{}
	}
	return copy
}

func cloneTemplate(value scenario.Template) scenario.Template {
	definition, err := scenario.ParseDefinition(value.Body)
	if err != nil {
		return scenario.Template{}
	}
	value.Definition = definition
	value.Body = append([]byte(nil), value.Body...)
	return value
}

func cloneEvents(events []engine.Event) []engine.Event {
	result := append([]engine.Event(nil), events...)
	for index := range result {
		result[index].Payload = append([]byte(nil), result[index].Payload...)
	}
	return result
}

func cloneSample(sample metric.Sample) metric.Sample {
	sample.Observations = append([]metric.Observation(nil), sample.Observations...)
	return sample
}

var _ Evaluator = Service{}
