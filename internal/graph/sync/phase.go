package sync

type WorkerPhase string

const (
	PhaseQueued                WorkerPhase = "QUEUED"
	PhaseValidationConfirmed   WorkerPhase = "VALIDATION_CONFIRMED"
	PhaseProjected             WorkerPhase = "PROJECTED"
	PhaseProviderCompatible    WorkerPhase = "PROVIDER_COMPATIBLE"
	PhaseSubmitting            WorkerPhase = "SUBMITTING"
	PhaseTaskAccepted          WorkerPhase = "TASK_ACCEPTED"
	PhasePolling               WorkerPhase = "POLLING"
	PhaseVerifying             WorkerPhase = "VERIFYING"
	PhaseReady                 WorkerPhase = "READY"
	PhaseImpactHandoffRecorded WorkerPhase = "IMPACT_HANDOFF_RECORDED"
)

func (p WorkerPhase) Valid() bool {
	switch p {
	case PhaseQueued, PhaseValidationConfirmed, PhaseProjected, PhaseProviderCompatible, PhaseSubmitting, PhaseTaskAccepted, PhasePolling, PhaseVerifying, PhaseReady, PhaseImpactHandoffRecorded:
		return true
	}
	return false
}

func (p WorkerPhase) CanAdvanceTo(next WorkerPhase) bool {
	if p == next {
		return true
	}
	// A provider may have forgotten an accepted Task while also having no
	// target Snapshot. The only safe recovery is a replay of the same
	// content-addressed PUT, which deliberately returns to SUBMITTING.
	if p == PhasePolling && next == PhaseSubmitting {
		return true
	}
	order := []WorkerPhase{PhaseQueued, PhaseValidationConfirmed, PhaseProjected, PhaseProviderCompatible, PhaseSubmitting, PhaseTaskAccepted, PhasePolling, PhaseVerifying, PhaseReady, PhaseImpactHandoffRecorded}
	for i, current := range order {
		if p == current {
			return i+1 < len(order) && next == order[i+1]
		}
	}
	return false
}
