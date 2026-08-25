package orchestration

import (
	"context"
	"encoding/json"
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
		Targets:          []ResolvedTarget{{EntityID: targetID, Kind: "character", EntityVersion: 7}},
		RequiredVersions: []aicontract.VersionIdentity{admissionVersion("validation"), admissionVersion("graph"), admissionVersion("simulation")},
	}}
	admission, err := NewV1Admission(source)
	if err != nil {
		t.Fatal(err)
	}
	request := AdmissionRequest{
		ProjectID: projectID, BaseRevisionID: revisionID,
		Goals:          []aicontract.Goal{{ID: "reduce-variance", Description: "Reduce variance."}},
		Metrics:        []aicontract.MetricGoal{{MetricID: "damage", Version: "v1", Direction: aicontract.MetricTarget, Target: "12", Unit: "points"}},
		Constraints:    []aicontract.Constraint{{ID: "keep-cost", Path: "/payload/cost", Operator: aicontract.ConstraintLessOrEqual, Value: json.RawMessage(`100`)}},
		AllowedTargets: []aicontract.AllowedTarget{{EntityID: targetID, Kind: "display-name-is-not-authoritative", ExpectedEntityVersion: 1, Paths: []aicontract.AllowedPath{{Path: "/payload/damage", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}}},
		Scenes:         []string{"default"},
	}
	first, err := admission.Admit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(source.selections) != 1 || len(source.selections[0].TargetIDs) != 1 || source.selections[0].TargetIDs[0] != targetID {
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

	request.AllowedTargets[0].Kind = "another-presentation-value"
	request.AllowedTargets[0].ExpectedEntityVersion = 99
	second, err := admission.Admit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.InputHash != second.InputHash || string(first.Canonical) != string(second.Canonical) {
		t.Fatal("caller-supplied kind/version changed canonical identity")
	}
}

func TestAdmissionPinsExplicitNoBaselineAndRequestedBudget(t *testing.T) {
	projectID := aicontract.ProjectID("018f9e40-0000-7000-8000-000000000111")
	revisionID := aicontract.RevisionID("018f9e40-0000-7000-8000-000000000112")
	targetID := aicontract.EntityID("018f9e40-0000-7000-8000-000000000113")
	source := &admissionSourceFake{snapshot: AdmissionSnapshot{
		Base:             aicontract.FrozenBaseIdentity{ProjectID: projectID, ConfigRevisionID: revisionID, ConfigHash: admissionHash("1"), VersionManifestHash: admissionHash("2"), MaterializationHash: admissionHash("3"), GraphNamespace: string(projectID), GraphSnapshot: string(revisionID), GraphContentHash: admissionHash("4")},
		Baseline:         aicontract.BaselineIdentity{Kind: aicontract.BaselineNone},
		Targets:          []ResolvedTarget{{EntityID: targetID, Kind: "skill", EntityVersion: 2}},
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
		AllowedTargets: []aicontract.AllowedTarget{{EntityID: targetID, Paths: []aicontract.AllowedPath{{Path: "/payload/cost", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}}},
		Scenes:         []string{"default"}, RequestedBudget: &budget,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Input.Baseline.Kind != aicontract.BaselineNone || result.Input.Budget.MaxToolCalls != budget.MaxToolCalls {
		t.Fatalf("resolved input=%#v", result.Input)
	}
}

func admissionVersion(id string) aicontract.VersionIdentity {
	return aicontract.VersionIdentity{ID: id, Version: "v1", Hash: admissionHash("f")}
}

func admissionHash(character string) aicontract.Hash {
	return aicontract.Hash(strings.Repeat(character, 64))
}
