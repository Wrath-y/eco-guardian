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
