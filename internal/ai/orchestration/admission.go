package orchestration

import (
	"context"
	"errors"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

var (
	ErrAdmissionUnavailable     = errors.New("AI design admission is unavailable")
	ErrAdmissionSnapshotInvalid = errors.New("AI design admission snapshot is invalid")
	ErrAdmissionInputInvalid    = errors.New("AI design input is invalid")
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
	TargetIDs      []aicontract.EntityID
}

// ResolvedTarget is an immutable entity fact from the selected revision.
type ResolvedTarget struct {
	EntityID      aicontract.EntityID
	Kind          string
	EntityVersion int64
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
		if !target.EntityID.Valid() || target.Kind == "" || target.EntityVersion < 1 {
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
	if a.Snapshots == nil || !request.ProjectID.Valid() || !request.BaseRevisionID.Valid() {
		return AdmittedInput{}, ErrAdmissionUnavailable
	}
	targetIDs := make([]aicontract.EntityID, len(request.AllowedTargets))
	for index, target := range request.AllowedTargets {
		targetIDs[index] = target.EntityID
	}
	snapshot, err := a.Snapshots.ResolveAIAdmissionSnapshot(ctx, AdmissionSelection{
		ProjectID: request.ProjectID, BaseRevisionID: request.BaseRevisionID, TargetIDs: targetIDs,
	})
	if err != nil {
		return AdmittedInput{}, err
	}
	if !snapshot.Valid() || snapshot.Base.ProjectID != request.ProjectID || snapshot.Base.ConfigRevisionID != request.BaseRevisionID {
		return AdmittedInput{}, ErrAdmissionSnapshotInvalid
	}

	budget, err := a.Registries.ResolvedLimits()
	if err != nil {
		return AdmittedInput{}, errors.Join(ErrAdmissionUnavailable, err)
	}
	if request.RequestedBudget != nil {
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

func resolveAllowedTargets(requested []aicontract.AllowedTarget, resolved []ResolvedTarget) []aicontract.AllowedTarget {
	byID := make(map[aicontract.EntityID]ResolvedTarget, len(resolved))
	for _, target := range resolved {
		byID[target.EntityID] = target
	}
	result := make([]aicontract.AllowedTarget, len(requested))
	for index, target := range requested {
		fact := byID[target.EntityID]
		result[index] = aicontract.AllowedTarget{EntityID: target.EntityID, Kind: fact.Kind, ExpectedEntityVersion: fact.EntityVersion, Paths: cloneAllowedPaths(target.Paths)}
	}
	return result
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
