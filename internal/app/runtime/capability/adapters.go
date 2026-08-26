package capability

import (
	"sort"
	"time"

	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/app/runtime/graphprocess"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
)

func ModuleObservation(id string, state State, generation uint64, observedAt time.Time, reasons ...string) Observation {
	result := Observation{ID: id, State: state, Generation: generation, ObservedAt: observedAt.UTC(), Reasons: []Reason{}}
	for _, code := range reasons {
		result.Reasons = append(result.Reasons, Reason{Code: code, Component: id, ObservationGeneration: generation})
	}
	sort.Slice(result.Reasons, func(left, right int) bool { return result.Reasons[left].Code < result.Reasons[right].Code })
	return result
}

func GraphObservations(observation graphprocess.HealthObservation) []Observation {
	observedAt := observation.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Unix(0, 0).UTC()
	}
	if observation.State == graphprocess.HealthUnavailable {
		reason := observation.Reason
		if reason == "" {
			reason = "GRAPH_HEALTH_UNAVAILABLE"
		}
		return []Observation{
			ModuleObservation(ObservationGraphSync, Unavailable, observation.Generation, observedAt, reason),
			ModuleObservation(ObservationRetrieval, Unavailable, observation.Generation, observedAt, reason),
		}
	}
	operation := func(id graphprocess.OperationID) graphprocess.OperationCompatibility {
		for _, value := range observation.Compatibility.Operations {
			if value.ID == id {
				return value
			}
		}
		return graphprocess.OperationCompatibility{ID: id, State: graphprocess.OperationUnavailable, Reasons: []string{"OPERATION_UNREGISTERED"}}
	}
	graphFTS := operation(graphprocess.OperationGraphFTSReadiness)
	return []Observation{
		graphOperationObservation(ObservationGraphSync, graphFTS, observation.Generation, observedAt),
		graphOperationObservation(ObservationRetrieval, observation.Compatibility.Retrieval, observation.Generation, observedAt),
	}
}

func graphOperationObservation(id string, operation graphprocess.OperationCompatibility, generation uint64, observedAt time.Time) Observation {
	state := Unavailable
	switch operation.State {
	case graphprocess.OperationAvailable:
		state = Available
	case graphprocess.OperationDegraded:
		state = Degraded
	}
	return ModuleObservation(id, state, generation, observedAt, operation.Reasons...)
}

func AIObservation(capability aiprovider.Capability, generation uint64, observedAt time.Time) Observation {
	state := Unavailable
	switch capability.State {
	case aiprovider.CapabilityAvailable:
		state = Available
	case aiprovider.CapabilityDegraded:
		state = Degraded
	}
	return ModuleObservation(ObservationAIProvider, state, generation, observedAt, capability.Reasons...)
}

func ReleaseGateObservation(capability versioninggate.ReleaseCapability, generation uint64, observedAt time.Time) Observation {
	if capability.Enabled {
		return ModuleObservation(ObservationReleaseGates, Available, generation, observedAt)
	}
	reasons := make([]string, 0, len(capability.Reasons))
	for _, reason := range capability.Reasons {
		code := "RELEASE_GATE_UNAVAILABLE"
		switch reason.Reason {
		case "required gate is unregistered":
			code = "RELEASE_GATE_UNREGISTERED"
		case "required gate contract is incompatible":
			code = "RELEASE_GATE_INCOMPATIBLE"
		case "gate registry unavailable":
			code = "RELEASE_GATE_REGISTRY_UNAVAILABLE"
		case "invalid release policy":
			code = "RELEASE_POLICY_INVALID"
		}
		reasons = append(reasons, code)
	}
	return ModuleObservation(ObservationReleaseGates, Unavailable, generation, observedAt, reasons...)
}
