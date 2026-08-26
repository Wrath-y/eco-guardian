package capability

import (
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

func releaseFixture(t *testing.T) (*versioninggate.Registry, versioningpolicy.ReleasePolicy, versioningrevision.CandidateContext, versioninggate.Result) {
	t.Helper()
	candidate := versioningrevision.CandidateContext{RevisionID: domain.ID("018f9e40-0000-7000-8000-000000000101"), ConfigHash: strings.Repeat("a", 64), ManifestHash: strings.Repeat("b", 64), PolicyID: domain.ID("018f9e40-0000-7000-8000-000000000102")}
	policy := versioningpolicy.ReleasePolicy{
		ID: candidate.PolicyID, DisplayVersion: 1, CanonicalHash: strings.Repeat("c", 64), CreatedAt: time.Now().UTC(),
		Definition: versioningpolicy.Definition{
			Samples: 10, ThresholdID: "threshold", ThresholdOn: true,
			Capabilities: []versioningpolicy.CapabilityRequirement{{CapabilityID: "validation", GateID: "full", ContractVersion: "1"}},
			Scenes: []versioningpolicy.Scene{
				{ID: "required", Required: true, Metrics: []versioningpolicy.Metric{{ID: "damage", Required: true}}},
				{ID: "optional", Required: false, Metrics: []versioningpolicy.Metric{{ID: "mana", Required: false}}},
			},
		},
	}
	descriptor := versioninggate.Descriptor{CapabilityID: "validation", GateID: "full", ContractVersion: "1", ImplementationVersion: "validation-v1", RequiredInputs: []string{"revision_id"}, SupportedStates: []versioninggate.ResultState{versioninggate.Pass, versioninggate.Warning, versioninggate.Block, versioninggate.Unavailable, versioninggate.Stale}}
	registry, err := versioninggate.NewRegistry(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	context := versioninggate.EvaluationContext{Candidate: candidate, PolicyHash: policy.CanonicalHash, SceneID: "required", MetricID: "damage", ThresholdID: "threshold", ImplementationVersions: map[string]string{"validation": "validation-v1"}}
	result := versioninggate.Result{ID: domain.ID("018f9e40-0000-7000-8000-000000000103"), Descriptor: descriptor, State: versioninggate.Pass, Evidence: []versioninggate.Evidence{}, ResultHash: strings.Repeat("d", 64), Context: context}
	if !policy.Valid() || !candidate.Valid() || !result.Valid() {
		t.Fatal("invalid release fixture")
	}
	return registry, policy, candidate, result
}

func TestReleaseObservationKeepsMissingUnregisteredAndStaleRequiredGatesExplicit(t *testing.T) {
	registry, policy, candidate, result := releaseFixture(t)
	now := time.Now().UTC()
	missing := EvaluateReleaseObservation(registry, policy, candidate, nil, 1, now)
	if missing.State != Unavailable || !hasReason(missing, "RELEASE_GATE_RESULT_MISSING") {
		t.Fatalf("missing=%#v", missing)
	}
	unregistered := EvaluateReleaseObservation(nil, policy, candidate, nil, 2, now)
	if unregistered.State != Unavailable || !hasReason(unregistered, "RELEASE_GATE_REGISTRY_UNAVAILABLE") {
		t.Fatalf("unregistered=%#v", unregistered)
	}
	result.Context.PolicyHash = strings.Repeat("e", 64)
	stale := EvaluateReleaseObservation(registry, policy, candidate, []versioninggate.Result{result}, 3, now)
	if stale.State != Unavailable || !hasReason(stale, "RELEASE_GATE_STALE") {
		t.Fatalf("stale=%#v", stale)
	}
}

func TestReleaseObservationPreservesOptionalWarningsWithoutRelaxingRequiredPolicy(t *testing.T) {
	registry, policy, candidate, result := releaseFixture(t)
	observation := EvaluateReleaseObservation(registry, policy, candidate, []versioninggate.Result{result}, 4, time.Now())
	if observation.State != Degraded || !hasReason(observation, "OPTIONAL_GATE_WARNING") {
		t.Fatalf("observation=%#v", observation)
	}
	result.State = versioninggate.Block
	blocked := EvaluateReleaseObservation(registry, policy, candidate, []versioninggate.Result{result}, 5, time.Now())
	if blocked.State != Unavailable || !hasReason(blocked, "RELEASE_GATE_BLOCKED") {
		t.Fatalf("blocked=%#v", blocked)
	}
}

func hasReason(observation Observation, code string) bool {
	for _, reason := range observation.Reasons {
		if reason.Code == code {
			return true
		}
	}
	return false
}
