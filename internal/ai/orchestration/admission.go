package orchestration

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/simulation/metric"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
)

const AIDesignJobKind sharedjob.Kind = "ai_design"

var (
	ErrAdmissionUnavailable     = errors.New("AI design admission is unavailable")
	ErrAdmissionSnapshotInvalid = errors.New("AI design admission snapshot is invalid")
	ErrAdmissionInputInvalid    = errors.New("AI design input is invalid")
	ErrAdmissionLimitExceeded   = errors.New("AI design admission limit exceeded")
	ErrAdmissionTargetInvalid   = errors.New("AI design target is invalid")
	ErrAdmissionScopeInvalid    = errors.New("AI design field scope is invalid")
	ErrAdmissionCompatibility   = errors.New("AI design scene or metric is incompatible")
)

const (
	maxAdmissionGoals       = 50
	maxAdmissionMetrics     = 100
	maxAdmissionConstraints = 200
	maxAdmissionTargets     = 200
	maxAdmissionPaths       = 200
	maxAdmissionScenes      = 100
	maxAdmissionGoalBytes   = 2_000
	maxAdmissionPathBytes   = 512
	maxAdmissionValueBytes  = 16_384
)

// AdmissionRequest contains only caller-controlled design intent. Stable
// revision, baseline and entity facts are deliberately resolved by SnapshotSource.
type AdmissionRequest struct {
	ProjectID       aicontract.ProjectID
	BaseRevisionID  aicontract.RevisionID
	Goals           []aicontract.Goal
	Metrics         []aicontract.MetricGoal
	Constraints     []aicontract.Constraint
	AllowedTargets  []aicontract.AllowedTarget
	Scenes          []string
	RequestedBudget *aicontract.Budget
}

type AdmissionSelection struct {
	ProjectID      aicontract.ProjectID
	BaseRevisionID aicontract.RevisionID
	Targets        []AdmissionTargetSelection
}

type AdmissionTargetSelection struct {
	EntityID aicontract.EntityID
	Paths    []aicontract.AllowedPath
}

// ResolvedTarget is an immutable entity fact from the selected revision.
type ResolvedTarget struct {
	EntityID      aicontract.EntityID
	Kind          string
	EntityVersion int64
	Paths         []aicontract.AllowedPath
}

// AdmissionSnapshot is detached from storage after one consistent read. Its
// source must resolve every field in one short read transaction so active
// release or revision facts cannot be mixed across database generations.
type AdmissionSnapshot struct {
	Base             aicontract.FrozenBaseIdentity
	Baseline         aicontract.BaselineIdentity
	Targets          []ResolvedTarget
	RequiredVersions []aicontract.VersionIdentity
}

func (v AdmissionSnapshot) Valid() bool {
	if !v.Base.Valid() || !v.Baseline.Valid() || len(v.Targets) == 0 || len(v.RequiredVersions) == 0 {
		return false
	}
	seenTargets := make(map[aicontract.EntityID]struct{}, len(v.Targets))
	for _, target := range v.Targets {
		candidate := aicontract.AllowedTarget{EntityID: target.EntityID, Kind: target.Kind, ExpectedEntityVersion: target.EntityVersion, Paths: target.Paths}
		if !candidate.Valid() {
			return false
		}
		if _, duplicate := seenTargets[target.EntityID]; duplicate {
			return false
		}
		seenTargets[target.EntityID] = struct{}{}
	}
	seenVersions := make(map[string]struct{}, len(v.RequiredVersions))
	for _, version := range v.RequiredVersions {
		if !version.Valid() {
			return false
		}
		if _, duplicate := seenVersions[version.ID]; duplicate {
			return false
		}
		seenVersions[version.ID] = struct{}{}
	}
	return true
}

type AdmissionSnapshotSource interface {
	ResolveAIAdmissionSnapshot(context.Context, AdmissionSelection) (AdmissionSnapshot, error)
}

type AdmittedInput struct {
	Input     aicontract.AIDesignInputV1
	Canonical []byte
	InputHash aicontract.Hash
}

type AIJobStore interface {
	CreateOrGet(context.Context, sharedjob.Request) (sharedjob.Record, bool, error)
}

type AdmissionResult struct {
	Job      sharedjob.Record
	Input    AdmittedInput
	Replayed bool
}

// Admission resolves storage facts exactly once, expands the registered v1
// budget and returns the only canonical input that may be attached to a Job.
type Admission struct {
	Snapshots  AdmissionSnapshotSource
	Registries aicontract.V1RegistrySet
}

func NewV1Admission(source AdmissionSnapshotSource) (Admission, error) {
	if source == nil {
		return Admission{}, ErrAdmissionUnavailable
	}
	registries, err := aicontract.NewV1RegistrySet()
	if err != nil {
		return Admission{}, errors.Join(ErrAdmissionUnavailable, err)
	}
	return Admission{Snapshots: source, Registries: registries}, nil
}

func (a Admission) Admit(ctx context.Context, request AdmissionRequest) (AdmittedInput, error) {
	if a.Snapshots == nil || !stableUUIDv7(string(request.ProjectID)) || !stableUUIDv7(string(request.BaseRevisionID)) {
		return AdmittedInput{}, ErrAdmissionUnavailable
	}
	if err := validateAdmissionRequest(request); err != nil {
		return AdmittedInput{}, err
	}
	targets := make([]AdmissionTargetSelection, len(request.AllowedTargets))
	for index, target := range request.AllowedTargets {
		targets[index] = AdmissionTargetSelection{EntityID: target.EntityID, Paths: cloneAllowedPaths(target.Paths)}
	}
	snapshot, err := a.Snapshots.ResolveAIAdmissionSnapshot(ctx, AdmissionSelection{
		ProjectID: request.ProjectID, BaseRevisionID: request.BaseRevisionID, Targets: targets,
	})
	if err != nil {
		return AdmittedInput{}, err
	}
	if !snapshot.Valid() || snapshot.Base.ProjectID != request.ProjectID || snapshot.Base.ConfigRevisionID != request.BaseRevisionID {
		return AdmittedInput{}, ErrAdmissionSnapshotInvalid
	}
	if !resolvedTargetsMatchRequest(request.AllowedTargets, snapshot.Targets) {
		return AdmittedInput{}, ErrAdmissionTargetInvalid
	}

	budget, err := a.Registries.ResolvedLimits()
	if err != nil {
		return AdmittedInput{}, errors.Join(ErrAdmissionUnavailable, err)
	}
	if request.RequestedBudget != nil {
		if !budgetWithinPolicy(*request.RequestedBudget, budget) {
			return AdmittedInput{}, ErrAdmissionLimitExceeded
		}
		budget = *request.RequestedBudget
	}
	fixture := aicontract.V1Fixture()
	input := aicontract.AIDesignInputV1{
		Schema: fixture.PatchSchema.Identity, Base: snapshot.Base, Baseline: snapshot.Baseline,
		Goals: cloneGoals(request.Goals), Metrics: cloneMetrics(request.Metrics), Constraints: cloneConstraints(request.Constraints),
		AllowedTargets: resolveAllowedTargets(request.AllowedTargets, snapshot.Targets), Scenes: append([]string(nil), request.Scenes...),
		Budget: budget, RequiredVersions: append([]aicontract.VersionIdentity(nil), snapshot.RequiredVersions...),
	}
	canonical, err := aicontract.CanonicalAIDesignInputV1(input)
	if err != nil {
		return AdmittedInput{}, errors.Join(ErrAdmissionInputInvalid, err)
	}
	hash, err := aicontract.HashAIDesignInputV1(input)
	if err != nil {
		return AdmittedInput{}, errors.Join(ErrAdmissionInputInvalid, err)
	}
	return AdmittedInput{Input: input, Canonical: canonical, InputHash: hash}, nil
}

// Submit creates runnable work only after the complete canonical input has
// been admitted. The existing shared Job store owns the atomic project/key
// decision; a replay must match every field represented by InputHash.
func (a Admission) Submit(ctx context.Context, jobs AIJobStore, request AdmissionRequest, idempotencyKey string) (AdmissionResult, error) {
	if jobs == nil {
		return AdmissionResult{}, ErrAdmissionUnavailable
	}
	input, err := a.Admit(ctx, request)
	if err != nil {
		return AdmissionResult{}, err
	}
	job, replayed, err := jobs.CreateOrGet(ctx, sharedjob.Request{
		ProjectID: domain.ID(request.ProjectID), Kind: AIDesignJobKind, RevisionID: domain.ID(request.BaseRevisionID),
		InputHash: string(input.InputHash), IdempotencyKey: idempotencyKey, RequestHash: string(input.InputHash),
	})
	if err != nil {
		return AdmissionResult{}, err
	}
	return AdmissionResult{Job: job, Input: input, Replayed: replayed}, nil
}

func resolveAllowedTargets(requested []aicontract.AllowedTarget, resolved []ResolvedTarget) []aicontract.AllowedTarget {
	byID := make(map[aicontract.EntityID]ResolvedTarget, len(resolved))
	for _, target := range resolved {
		byID[target.EntityID] = target
	}
	result := make([]aicontract.AllowedTarget, len(requested))
	for index, target := range requested {
		fact := byID[target.EntityID]
		result[index] = aicontract.AllowedTarget{EntityID: target.EntityID, Kind: fact.Kind, ExpectedEntityVersion: fact.EntityVersion, Paths: cloneAllowedPaths(fact.Paths)}
	}
	return result
}

func validateAdmissionRequest(request AdmissionRequest) error {
	if len(request.Goals) < 1 || len(request.Goals) > maxAdmissionGoals || len(request.Metrics) < 1 || len(request.Metrics) > maxAdmissionMetrics ||
		len(request.Constraints) > maxAdmissionConstraints || len(request.AllowedTargets) < 1 || len(request.AllowedTargets) > maxAdmissionTargets || len(request.Scenes) < 1 || len(request.Scenes) > maxAdmissionScenes {
		return ErrAdmissionLimitExceeded
	}
	seenGoals := map[string]struct{}{}
	for _, goal := range request.Goals {
		if !goal.Valid() || !utf8.ValidString(goal.Description) || len(goal.Description) > maxAdmissionGoalBytes {
			return ErrAdmissionInputInvalid
		}
		if _, duplicate := seenGoals[goal.ID]; duplicate {
			return ErrAdmissionInputInvalid
		}
		seenGoals[goal.ID] = struct{}{}
	}
	seenConstraints := map[string]struct{}{}
	for _, constraint := range request.Constraints {
		if !constraint.Valid() || len(constraint.Path) > maxAdmissionPathBytes || len(constraint.Value) > maxAdmissionValueBytes {
			return ErrAdmissionLimitExceeded
		}
		if _, duplicate := seenConstraints[constraint.ID]; duplicate {
			return ErrAdmissionInputInvalid
		}
		seenConstraints[constraint.ID] = struct{}{}
	}
	seenTargets := map[aicontract.EntityID]struct{}{}
	for _, target := range request.AllowedTargets {
		if !stableUUIDv7(string(target.EntityID)) || target.Kind == "" || target.ExpectedEntityVersion < 1 || len(target.Paths) < 1 || len(target.Paths) > maxAdmissionPaths {
			return ErrAdmissionTargetInvalid
		}
		if _, duplicate := seenTargets[target.EntityID]; duplicate {
			return ErrAdmissionTargetInvalid
		}
		seenTargets[target.EntityID] = struct{}{}
		seenPaths := map[aicontract.FieldPath]struct{}{}
		for _, path := range target.Paths {
			if !path.Valid() || len(path.Path) > maxAdmissionPathBytes {
				return ErrAdmissionScopeInvalid
			}
			if _, duplicate := seenPaths[path.Path]; duplicate {
				return ErrAdmissionScopeInvalid
			}
			seenPaths[path.Path] = struct{}{}
		}
	}
	if !v1ScenesAndMetricsCompatible(request.Scenes, request.Metrics) {
		return ErrAdmissionCompatibility
	}
	return nil
}

func stableUUIDv7(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.Version() == 7
}

func v1ScenesAndMetricsCompatible(scenes []string, metrics []aicontract.MetricGoal) bool {
	availableScenes := map[string]struct{}{}
	for _, template := range scenario.BuiltinTemplates() {
		availableScenes[template.Definition.ID] = struct{}{}
	}
	seenScenes := map[string]struct{}{}
	for _, sceneID := range scenes {
		if _, found := availableScenes[sceneID]; !found || strings.TrimSpace(sceneID) != sceneID {
			return false
		}
		if _, duplicate := seenScenes[sceneID]; duplicate {
			return false
		}
		seenScenes[sceneID] = struct{}{}
	}
	availableMetrics := map[string]metric.Descriptor{}
	for _, module := range metric.V1Modules() {
		descriptor := module.Descriptor()
		availableMetrics[descriptor.ID] = descriptor
	}
	seenMetrics := map[string]struct{}{}
	for _, goal := range metrics {
		descriptor, found := availableMetrics[goal.MetricID]
		if !found || !goal.Valid() || goal.Version != descriptor.Version || goal.Unit != descriptor.Unit {
			return false
		}
		if _, duplicate := seenMetrics[goal.MetricID]; duplicate {
			return false
		}
		seenMetrics[goal.MetricID] = struct{}{}
	}
	return true
}

func resolvedTargetsMatchRequest(requested []aicontract.AllowedTarget, resolved []ResolvedTarget) bool {
	if len(requested) != len(resolved) {
		return false
	}
	byID := make(map[aicontract.EntityID]ResolvedTarget, len(resolved))
	for _, target := range resolved {
		byID[target.EntityID] = target
	}
	for _, target := range requested {
		fact, found := byID[target.EntityID]
		if !found || target.Kind != fact.Kind || target.ExpectedEntityVersion != fact.EntityVersion || !sameAllowedPaths(target.Paths, fact.Paths) {
			return false
		}
	}
	return true
}

func sameAllowedPaths(left, right []aicontract.AllowedPath) bool {
	if len(left) != len(right) {
		return false
	}
	byPath := make(map[aicontract.FieldPath]map[aicontract.PatchOperationKind]struct{}, len(right))
	for _, path := range right {
		operations := make(map[aicontract.PatchOperationKind]struct{}, len(path.Operations))
		for _, operation := range path.Operations {
			operations[operation] = struct{}{}
		}
		byPath[path.Path] = operations
	}
	for _, path := range left {
		operations, found := byPath[path.Path]
		if !found || len(operations) != len(path.Operations) {
			return false
		}
		for _, operation := range path.Operations {
			if _, found = operations[operation]; !found {
				return false
			}
		}
	}
	return true
}

func budgetWithinPolicy(requested, policy aicontract.Budget) bool {
	if !requested.Valid() || requested.Policy != policy.Policy || requested.MaxFormatRepairs != policy.MaxFormatRepairs {
		return false
	}
	return requested.MaxProviderTurns <= policy.MaxProviderTurns && requested.MaxToolCalls <= policy.MaxToolCalls &&
		requested.MaxSearchCandidates <= policy.MaxSearchCandidates && requested.MaxDurationMillis <= policy.MaxDurationMillis &&
		requested.MaxContextBytes <= policy.MaxContextBytes && requested.MaxOutputBytes <= policy.MaxOutputBytes &&
		requested.MaxToolResultBytes <= policy.MaxToolResultBytes && requested.RetrievalSeedLimit <= policy.RetrievalSeedLimit &&
		requested.RetrievalResultLimit <= policy.RetrievalResultLimit && requested.RetrievalGraphDepth <= policy.RetrievalGraphDepth
}

func cloneGoals(values []aicontract.Goal) []aicontract.Goal {
	return append([]aicontract.Goal(nil), values...)
}

func cloneMetrics(values []aicontract.MetricGoal) []aicontract.MetricGoal {
	return append([]aicontract.MetricGoal(nil), values...)
}

func cloneConstraints(values []aicontract.Constraint) []aicontract.Constraint {
	result := append([]aicontract.Constraint(nil), values...)
	for index := range result {
		result[index].Value = append([]byte(nil), result[index].Value...)
	}
	return result
}

func cloneAllowedPaths(values []aicontract.AllowedPath) []aicontract.AllowedPath {
	result := append([]aicontract.AllowedPath(nil), values...)
	for index := range result {
		result[index].Operations = append([]aicontract.PatchOperationKind(nil), result[index].Operations...)
	}
	return result
}
