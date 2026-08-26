package capability

import (
	"reflect"
	"testing"
	"time"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/app/runtime/graphprocess"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
)

func TestDefaultRegistryContainsAppliedCapabilityProjection(t *testing.T) {
	registry := DefaultRegistry()
	results := registry.Evaluate(map[string]Observation{})
	want := []string{CapabilityAIDesign, CapabilityBackup, CapabilityGraphSync, CapabilityDeterministicImpact, CapabilityLocalEditing, CapabilityRelease, CapabilityRetrieval, CapabilityRevisionBrowsing, CapabilityDeterministicSimulation, CapabilityValidation}
	got := make([]string, len(results))
	for index, result := range results {
		got[index] = result.ID
		if result.State != Unavailable {
			t.Fatalf("missing observation inferred available: %#v", result)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ids=%v want=%v", got, want)
	}
}

func TestAppliedModuleAdaptersFeedOneRegistryWithoutOwningAdmission(t *testing.T) {
	now := time.Date(2026, 8, 25, 4, 0, 0, 0, time.UTC)
	health := graphprocess.HealthObservation{Generation: 4, ObservedAt: now, Compatibility: graphprocess.HealthCompatibility{
		Operations: []graphprocess.OperationCompatibility{{ID: graphprocess.OperationGraphFTSReadiness, State: graphprocess.OperationAvailable}},
		Retrieval:  graphprocess.OperationCompatibility{ID: "retrieval", State: graphprocess.OperationDegraded, Reasons: []string{"DEPENDENCY_vector_DEGRADED"}},
	}}
	observations := map[string]Observation{}
	for _, value := range GraphObservations(health) {
		observations[value.ID] = value
	}
	for _, id := range []string{ObservationLocalEditing, ObservationValidation, ObservationRevision, ObservationSimulation, ObservationBackup, ObservationImpact} {
		observations[id] = ModuleObservation(id, Available, 2, now)
	}
	observations[ObservationAIProvider] = AIObservation(aiprovider.Capability{State: aiprovider.CapabilityAvailable}, 3, now)
	observations[ObservationReleaseGates] = ReleaseGateObservation(versioninggate.ReleaseCapability{Enabled: true, Reasons: []versioninggate.DisabledReason{}}, 5, now)
	results := DefaultRegistry().Evaluate(observations)
	states := map[string]State{}
	for _, result := range results {
		states[result.ID] = result.State
	}
	if states[CapabilityLocalEditing] != Available || states[CapabilityGraphSync] != Available || states[CapabilityRetrieval] != Degraded || states[CapabilityAIDesign] != Degraded || states[CapabilityRelease] != Available {
		t.Fatalf("states=%v", states)
	}
}

func TestReleaseAndAIAdaptersPreserveOnlyStableReasons(t *testing.T) {
	now := time.Now()
	release := ReleaseGateObservation(versioninggate.ReleaseCapability{Reasons: []versioninggate.DisabledReason{{CapabilityID: "graph", GateID: "projection", Reason: "required gate is unregistered"}}}, 7, now)
	if release.State != Unavailable || release.Reasons[0].Code != "RELEASE_GATE_UNREGISTERED" {
		t.Fatalf("release=%#v", release)
	}
	ai := AIObservation(aiprovider.Capability{State: aiprovider.CapabilityUnavailable, Reasons: []string{aiprovider.ReasonCredentialUnavailable}}, 8, now)
	if ai.State != Unavailable || ai.Reasons[0].Code != aiprovider.ReasonCredentialUnavailable {
		t.Fatalf("ai=%#v", ai)
	}
}

func TestDefaultDegradationMatrixPreservesOfflineAndIndependentCapabilities(t *testing.T) {
	now := time.Now().UTC()
	base := map[string]Observation{}
	for _, id := range []string{ObservationLocalEditing, ObservationValidation, ObservationRevision, ObservationSimulation, ObservationBackup, ObservationImpact, ObservationGraphSync, ObservationRetrieval, ObservationAIProvider, ObservationReleaseGates} {
		base[id] = ModuleObservation(id, Available, 1, now)
	}
	tests := []struct {
		name   string
		mutate func(map[string]Observation)
		want   map[string]State
	}{
		{
			name: "Graph core or FTS loss",
			mutate: func(values map[string]Observation) {
				values[ObservationGraphSync] = ModuleObservation(ObservationGraphSync, Unavailable, 2, now, "GRAPH_FTS_UNAVAILABLE")
			},
			want: map[string]State{CapabilityLocalEditing: Available, CapabilityValidation: Available, CapabilityRevisionBrowsing: Available, CapabilityDeterministicSimulation: Available, CapabilityBackup: Available, CapabilityGraphSync: Unavailable, CapabilityDeterministicImpact: Unavailable, CapabilityRetrieval: Unavailable, CapabilityAIDesign: Unavailable, CapabilityRelease: Unavailable},
		},
		{
			name: "Vector or Rerank loss",
			mutate: func(values map[string]Observation) {
				values[ObservationRetrieval] = ModuleObservation(ObservationRetrieval, Degraded, 2, now, "VECTOR_RETRIEVAL_UNAVAILABLE")
			},
			want: map[string]State{CapabilityGraphSync: Available, CapabilityDeterministicImpact: Available, CapabilityRetrieval: Degraded, CapabilityAIDesign: Degraded, CapabilityRelease: Available, CapabilityDeterministicSimulation: Available},
		},
		{
			name: "AI Provider loss",
			mutate: func(values map[string]Observation) {
				values[ObservationAIProvider] = ModuleObservation(ObservationAIProvider, Unavailable, 2, now, "AI_PROVIDER_UNAVAILABLE")
			},
			want: map[string]State{CapabilityAIDesign: Unavailable, CapabilityRetrieval: Available, CapabilityGraphSync: Available, CapabilityDeterministicImpact: Available, CapabilityRelease: Available, CapabilityLocalEditing: Available},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observations := cloneObservations(base)
			test.mutate(observations)
			states := map[string]State{}
			for _, result := range DefaultRegistry().Evaluate(observations) {
				states[result.ID] = result.State
			}
			for id, want := range test.want {
				if states[id] != want {
					t.Fatalf("capability %s=%s want=%s all=%v", id, states[id], want, states)
				}
			}
		})
	}
}

func TestMultipleFailuresRecoverIndependently(t *testing.T) {
	now := time.Now().UTC()
	observations := map[string]Observation{}
	for _, id := range []string{ObservationLocalEditing, ObservationValidation, ObservationRevision, ObservationSimulation, ObservationBackup, ObservationImpact, ObservationGraphSync, ObservationRetrieval, ObservationAIProvider, ObservationReleaseGates} {
		observations[id] = ModuleObservation(id, Available, 1, now)
	}
	observations[ObservationGraphSync] = ModuleObservation(ObservationGraphSync, Unavailable, 2, now, "GRAPH_CORE_UNAVAILABLE")
	observations[ObservationAIProvider] = ModuleObservation(ObservationAIProvider, Unavailable, 3, now, "AI_PROVIDER_UNAVAILABLE")
	failed := resultStates(DefaultRegistry().Evaluate(observations))
	if failed[CapabilityGraphSync] != Unavailable || failed[CapabilityRetrieval] != Unavailable || failed[CapabilityAIDesign] != Unavailable || failed[CapabilityLocalEditing] != Available {
		t.Fatalf("simultaneous failure states=%v", failed)
	}
	observations[ObservationGraphSync] = ModuleObservation(ObservationGraphSync, Available, 4, now.Add(time.Second))
	recovered := resultStates(DefaultRegistry().Evaluate(observations))
	if recovered[CapabilityGraphSync] != Available || recovered[CapabilityRetrieval] != Available || recovered[CapabilityAIDesign] != Unavailable || recovered[CapabilityRelease] != Available {
		t.Fatalf("independent recovery states=%v", recovered)
	}
}

func resultStates(results []Result) map[string]State {
	states := make(map[string]State, len(results))
	for _, result := range results {
		states[result.ID] = result.State
	}
	return states
}
