package capability

const (
	CapabilityLocalEditing            = "local.editing"
	CapabilityValidation              = "validation"
	CapabilityRevisionBrowsing        = "revision.browsing"
	CapabilityDeterministicSimulation = "simulation.deterministic"
	CapabilityBackup                  = "backup"
	CapabilityGraphSync               = "graph.sync"
	CapabilityDeterministicImpact     = "impact.deterministic"
	CapabilityRetrieval               = "retrieval"
	CapabilityAIDesign                = "ai.design"
	CapabilityRelease                 = "release"
)

const (
	ObservationLocalEditing = "module.local-editing"
	ObservationValidation   = "module.validation"
	ObservationRevision     = "module.revision"
	ObservationSimulation   = "module.simulation"
	ObservationBackup       = "module.backup"
	ObservationGraphSync    = "graph.sync"
	ObservationImpact       = "module.impact"
	ObservationRetrieval    = "graph.retrieval"
	ObservationAIProvider   = "ai.provider"
	ObservationReleaseGates = "release.gates"
)

func DefaultRegistry() *Registry {
	descriptors := []Descriptor{
		observationDescriptor(CapabilityLocalEditing, ObservationLocalEditing),
		observationDescriptor(CapabilityValidation, ObservationValidation),
		observationDescriptor(CapabilityRevisionBrowsing, ObservationRevision),
		observationDescriptor(CapabilityDeterministicSimulation, ObservationSimulation),
		observationDescriptor(CapabilityBackup, ObservationBackup),
		observationDescriptor(CapabilityGraphSync, ObservationGraphSync),
		{
			ID: CapabilityDeterministicImpact, Version: "1", Prerequisites: []Prerequisite{{CapabilityID: CapabilityGraphSync, Required: true}},
			Evaluator: observationEvaluator(ObservationImpact),
		},
		{
			ID: CapabilityRetrieval, Version: "1", Prerequisites: []Prerequisite{{CapabilityID: CapabilityGraphSync, Required: true}},
			Evaluator: observationEvaluator(ObservationRetrieval),
		},
		{
			ID: CapabilityAIDesign, Version: "1", Prerequisites: []Prerequisite{{CapabilityID: CapabilityRetrieval, Required: true}},
			Evaluator: observationEvaluator(ObservationAIProvider),
		},
		{
			ID: CapabilityRelease, Version: "1",
			Prerequisites: []Prerequisite{
				{CapabilityID: CapabilityValidation, Required: true}, {CapabilityID: CapabilityRevisionBrowsing, Required: true},
				{CapabilityID: CapabilityBackup, Required: true}, {CapabilityID: CapabilityGraphSync, Required: true},
			},
			Evaluator: observationEvaluator(ObservationReleaseGates),
		},
	}
	registry, err := NewRegistry(descriptors...)
	if err != nil {
		panic(err)
	}
	return registry
}

func observationDescriptor(capabilityID, observationID string) Descriptor {
	return Descriptor{ID: capabilityID, Version: "1", Evaluator: observationEvaluator(observationID)}
}

func observationEvaluator(observationID string) Evaluator {
	return func(context EvaluationContext) Result {
		observation, ok := context.Observations[observationID]
		if !ok {
			return Result{State: Unavailable, Reasons: []Reason{{Code: "OBSERVATION_UNREGISTERED", Component: observationID}}, Actions: []Action{}}
		}
		return Result{State: observation.State, Reasons: append([]Reason(nil), observation.Reasons...), Actions: []Action{}, ObservationGeneration: observation.Generation}
	}
}
