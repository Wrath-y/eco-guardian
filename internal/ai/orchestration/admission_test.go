package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
)

type admissionSourceFake struct {
	snapshot   AdmissionSnapshot
	selections []AdmissionSelection
}

func (s *admissionSourceFake) ResolveAIAdmissionSnapshot(_ context.Context, selection AdmissionSelection) (AdmissionSnapshot, error) {
	s.selections = append(s.selections, selection)
	return s.snapshot, nil
}

func TestAdmissionPinsOneAtomicSnapshotAndCanonicalizesResolvedFacts(t *testing.T) {
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000101")
	revisionID := aicontract.RevisionID("018f9e40-0000-7000-8000-000000000102")
	targetID := aicontract.EntityID("018f9e40-0000-7000-8000-000000000103")
	baselineRevision := aicontract.RevisionID("018f9e40-0000-7000-8000-000000000104")
	releaseID := aicontract.ReleaseID("018f9e40-0000-7000-8000-000000000105")
	source := &admissionSourceFake{snapshot: AdmissionSnapshot{
		Base: aicontract.FrozenBaseIdentity{
			ProjectID: projectID, ConfigRevisionID: revisionID, ConfigHash: admissionHash("a"), VersionManifestHash: admissionHash("b"),
			MaterializationHash: admissionHash("c"), GraphNamespace: string(projectID), GraphSnapshot: string(revisionID), GraphContentHash: admissionHash("d"),
		},
		Baseline:         aicontract.BaselineIdentity{Kind: aicontract.BaselineCurrent, ReleaseID: releaseID, ConfigRevisionID: baselineRevision, ConfigHash: admissionHash("e")},
		Targets:          []ResolvedTarget{{EntityID: targetID, Kind: "character", EntityVersion: 7, Paths: []aicontract.AllowedPath{{Path: "/payload/damage", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}}},
		RequiredVersions: []aicontract.VersionIdentity{admissionVersion("validation"), admissionVersion("graph"), admissionVersion("simulation")},
	}}
	admission, err := NewV1Admission(source)
	if err != nil {
		t.Fatal(err)
	}
	request := AdmissionRequest{
		ProjectID: projectID, BaseRevisionID: revisionID,
		Goals:          []aicontract.Goal{{ID: "reduce-variance", Description: "Reduce variance."}},
		Metrics:        []aicontract.MetricGoal{{MetricID: "metric-dps", Version: "v1", Direction: aicontract.MetricTarget, Target: "12", Unit: "points_per_second"}},
		Constraints:    []aicontract.Constraint{{ID: "keep-cost", Path: "/payload/cost", Operator: aicontract.ConstraintLessOrEqual, Value: json.RawMessage(`100`)}},
		AllowedTargets: []aicontract.AllowedTarget{{EntityID: targetID, Kind: "character", ExpectedEntityVersion: 7, Paths: []aicontract.AllowedPath{{Path: "/payload/damage", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}}},
		Scenes:         []string{"single-target-30s"},
	}
	first, err := admission.Admit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.selections) != 1 || len(source.selections[0].Targets) != 1 || source.selections[0].Targets[0].EntityID != targetID {
		t.Fatalf("atomic selection=%#v", source.selections)
	}
	if got := first.Input.AllowedTargets[0]; got.Kind != "character" || got.ExpectedEntityVersion != 7 {
		t.Fatalf("target was not resolved from immutable snapshot: %#v", got)
	}
	limits, err := admission.Registries.ResolvedLimits()
	if err != nil || first.Input.Budget != limits {
		t.Fatalf("budget was not expanded: got=%#v want=%#v err=%v", first.Input.Budget, limits, err)
	}
	if len(first.Canonical) == 0 || !first.InputHash.Valid() {
		t.Fatalf("canonical admission missing: %#v", first)
	}

	request.AllowedTargets[0].Kind = "display-name-is-not-an-identity"
	request.AllowedTargets[0].ExpectedEntityVersion = 99
	if _, err = admission.Admit(context.Background(), request); !errors.Is(err, ErrAdmissionTargetInvalid) {
		t.Fatalf("stale/display target err=%v", err)
	}
}

func TestAdmissionPinsExplicitNoBaselineAndRequestedBudget(t *testing.T) {
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000111")
	revisionID := aicontract.RevisionID("018f9e40-0000-7000-8000-000000000112")
	targetID := aicontract.EntityID("018f9e40-0000-7000-8000-000000000113")
	source := &admissionSourceFake{snapshot: AdmissionSnapshot{
		Base:             aicontract.FrozenBaseIdentity{ProjectID: projectID, ConfigRevisionID: revisionID, ConfigHash: admissionHash("1"), VersionManifestHash: admissionHash("2"), MaterializationHash: admissionHash("3"), GraphNamespace: string(projectID), GraphSnapshot: string(revisionID), GraphContentHash: admissionHash("4")},
		Baseline:         aicontract.BaselineIdentity{Kind: aicontract.BaselineNone},
		Targets:          []ResolvedTarget{{EntityID: targetID, Kind: "skill", EntityVersion: 2, Paths: []aicontract.AllowedPath{{Path: "/payload/cost", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}}},
		RequiredVersions: []aicontract.VersionIdentity{admissionVersion("validation")},
	}}
	admission, err := NewV1Admission(source)
	if err != nil {
		t.Fatal(err)
	}
	budget, _ := admission.Registries.ResolvedLimits()
	budget.MaxToolCalls--
	result, err := admission.Admit(context.Background(), AdmissionRequest{
		ProjectID: projectID, BaseRevisionID: revisionID, Goals: []aicontract.Goal{{ID: "goal", Description: "Tune skill."}},
		Metrics:        []aicontract.MetricGoal{{MetricID: "metric-dps", Version: "v1", Direction: aicontract.MetricMinimize, Unit: "points_per_second"}},
		AllowedTargets: []aicontract.AllowedTarget{{EntityID: targetID, Kind: "skill", ExpectedEntityVersion: 2, Paths: []aicontract.AllowedPath{{Path: "/payload/cost", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}}},
		Scenes:         []string{"single-target-30s"}, RequestedBudget: &budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Input.Baseline.Kind != aicontract.BaselineNone || result.Input.Budget.MaxToolCalls != budget.MaxToolCalls {
		t.Fatalf("resolved input=%#v", result.Input)
	}
}

func TestAdmissionRejectsInvalidIntentBeforeSnapshotOrExternalWork(t *testing.T) {
	admission, source, request := validAdmissionTestFixture(t)
	tests := []struct {
		name   string
		mutate func(*AdmissionRequest)
		want   error
	}{
		{"display name identity", func(value *AdmissionRequest) { value.AllowedTargets[0].EntityID = "damage" }, ErrAdmissionTargetInvalid},
		{"duplicate target", func(value *AdmissionRequest) {
			value.AllowedTargets = append(value.AllowedTargets, value.AllowedTargets[0])
		}, ErrAdmissionTargetInvalid},
		{"invalid operation", func(value *AdmissionRequest) { value.AllowedTargets[0].Paths[0].Operations[0] = "execute" }, ErrAdmissionScopeInvalid},
		{"unknown scene", func(value *AdmissionRequest) { value.Scenes[0] = "display-scene" }, ErrAdmissionCompatibility},
		{"metric unit mismatch", func(value *AdmissionRequest) { value.Metrics[0].Unit = "points" }, ErrAdmissionCompatibility},
		{"goal hard limit", func(value *AdmissionRequest) {
			value.Goals[0].Description = strings.Repeat("x", maxAdmissionGoalBytes+1)
		}, ErrAdmissionInputInvalid},
		{"constraint value hard limit", func(value *AdmissionRequest) {
			value.Constraints = []aicontract.Constraint{{ID: "huge", Path: "/payload/cost", Operator: aicontract.ConstraintEqual, Value: json.RawMessage(`"` + strings.Repeat("x", maxAdmissionValueBytes) + `"`)}}
		}, ErrAdmissionLimitExceeded},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneAdmissionRequest(request)
			test.mutate(&candidate)
			before := len(source.selections)
			if _, err := admission.Admit(context.Background(), candidate); !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
			if len(source.selections) != before {
				t.Fatal("invalid request reached snapshot source")
			}
		})
	}
}

func TestAdmissionRejectsStaleTargetsAndBudgetAboveRegisteredPolicy(t *testing.T) {
	admission, _, request := validAdmissionTestFixture(t)
	stale := cloneAdmissionRequest(request)
	stale.AllowedTargets[0].ExpectedEntityVersion++
	if _, err := admission.Admit(context.Background(), stale); !errors.Is(err, ErrAdmissionTargetInvalid) {
		t.Fatalf("stale target err=%v", err)
	}
	budget, _ := admission.Registries.ResolvedLimits()
	budget.MaxToolCalls++
	request.RequestedBudget = &budget
	if _, err := admission.Admit(context.Background(), request); !errors.Is(err, ErrAdmissionLimitExceeded) {
		t.Fatalf("budget err=%v", err)
	}
}

func validAdmissionTestFixture(t *testing.T) (Admission, *admissionSourceFake, AdmissionRequest) {
	t.Helper()
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000121")
	revisionID := aicontract.RevisionID("018f9e40-0000-7000-8000-000000000122")
	targetID := aicontract.EntityID("018f9e40-0000-7000-8000-000000000123")
	paths := []aicontract.AllowedPath{{Path: "/payload/cost", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}
	source := &admissionSourceFake{snapshot: AdmissionSnapshot{
		Base:     aicontract.FrozenBaseIdentity{ProjectID: projectID, ConfigRevisionID: revisionID, ConfigHash: admissionHash("1"), VersionManifestHash: admissionHash("2"), MaterializationHash: admissionHash("3"), GraphNamespace: string(projectID), GraphSnapshot: string(revisionID), GraphContentHash: admissionHash("4")},
		Baseline: aicontract.BaselineIdentity{Kind: aicontract.BaselineNone}, Targets: []ResolvedTarget{{EntityID: targetID, Kind: "skill", EntityVersion: 2, Paths: cloneAllowedPaths(paths)}},
		RequiredVersions: []aicontract.VersionIdentity{admissionVersion("validation")},
	}}
	admission, err := NewV1Admission(source)
	if err != nil {
		t.Fatal(err)
	}
	request := AdmissionRequest{
		ProjectID: projectID, BaseRevisionID: revisionID, Goals: []aicontract.Goal{{ID: "goal", Description: "Tune skill."}},
		Metrics:        []aicontract.MetricGoal{{MetricID: "metric-dps", Version: "v1", Direction: aicontract.MetricMinimize, Unit: "points_per_second"}},
		AllowedTargets: []aicontract.AllowedTarget{{EntityID: targetID, Kind: "skill", ExpectedEntityVersion: 2, Paths: cloneAllowedPaths(paths)}}, Scenes: []string{"single-target-30s"},
	}
	return admission, source, request
}

func cloneAdmissionRequest(value AdmissionRequest) AdmissionRequest {
	value.Goals = append([]aicontract.Goal(nil), value.Goals...)
	value.Metrics = append([]aicontract.MetricGoal(nil), value.Metrics...)
	value.Constraints = cloneConstraints(value.Constraints)
	value.AllowedTargets = append([]aicontract.AllowedTarget(nil), value.AllowedTargets...)
	for index := range value.AllowedTargets {
		value.AllowedTargets[index].Paths = cloneAllowedPaths(value.AllowedTargets[index].Paths)
	}
	value.Scenes = append([]string(nil), value.Scenes...)
	return value
}

func admissionVersion(id string) aicontract.VersionIdentity {
	return aicontract.VersionIdentity{ID: id, Version: "v1", Hash: admissionHash("f")}
}

func admissionHash(character string) aicontract.Hash {
	return aicontract.Hash(strings.Repeat(character, 64))
}
