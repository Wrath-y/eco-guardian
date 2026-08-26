package recovery

import (
	"context"

	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	riskorchestration "github.com/zouyi/eco-guardian/internal/risk/orchestration"
	simulationorchestration "github.com/zouyi/eco-guardian/internal/simulation/orchestration"
)

type ImpactRecoverer interface {
	Recover(context.Context) ([]graphsync.ImpactRecoveryResult, error)
}

type ImpactAdapter struct{ Recovery ImpactRecoverer }

func (adapter ImpactAdapter) ScanAndRecover(ctx context.Context) (ScanResult, error) {
	if adapter.Recovery == nil {
		return ScanResult{}, ErrRegistryInvalid
	}
	results, err := adapter.Recovery.Recover(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	summary := ScanResult{Scanned: len(results)}
	for _, result := range results {
		if result.Dispatched {
			summary.Recovered++
		} else {
			summary.RecoveryRequired++
		}
	}
	return summary, nil
}

type SimulationRecoverer interface {
	RecoverAll(context.Context) ([]simulationorchestration.BatchRecoveryResult, error)
}

type SimulationAdapter struct{ Recovery SimulationRecoverer }

func (adapter SimulationAdapter) ScanAndRecover(ctx context.Context) (ScanResult, error) {
	if adapter.Recovery == nil {
		return ScanResult{}, ErrRegistryInvalid
	}
	results, err := adapter.Recovery.RecoverAll(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	summary := ScanResult{Scanned: len(results)}
	for _, result := range results {
		if result.RecoveryRequired {
			summary.RecoveryRequired++
		} else {
			summary.Recovered++
		}
	}
	return summary, nil
}

type RiskRecoverer interface {
	RecoverAll(context.Context) ([]riskorchestration.RecoveryResult, error)
}

type RiskAdapter struct{ Recovery RiskRecoverer }

func (adapter RiskAdapter) ScanAndRecover(ctx context.Context) (ScanResult, error) {
	if adapter.Recovery == nil {
		return ScanResult{}, ErrRegistryInvalid
	}
	results, err := adapter.Recovery.RecoverAll(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	summary := ScanResult{Scanned: len(results)}
	for _, result := range results {
		if result.Error != "" || (result.Status != sharedjob.Succeeded && result.Status != sharedjob.Queued && result.Status != sharedjob.Running && result.Status != sharedjob.Interrupted) {
			summary.RecoveryRequired++
		} else {
			summary.Recovered++
		}
	}
	return summary, nil
}

// DeterministicStageAdapter retains the required impact → simulation → risk
// order while each module remains the owner of fingerprint, checkpoint, seal,
// and result-identity validation.
type DeterministicStageAdapter struct {
	Impact, Simulation, Risk Scanner
}

func (adapter DeterministicStageAdapter) ScanAndRecover(ctx context.Context) (ScanResult, error) {
	total := ScanResult{}
	for _, scanner := range []Scanner{adapter.Impact, adapter.Simulation, adapter.Risk} {
		if scanner == nil {
			continue
		}
		result, err := scanner.ScanAndRecover(ctx)
		if err != nil {
			return total, err
		}
		total.Scanned += result.Scanned
		total.Recovered += result.Recovered
		total.RecoveryRequired += result.RecoveryRequired
	}
	return total, nil
}

var _ Scanner = ImpactAdapter{}
var _ Scanner = SimulationAdapter{}
var _ Scanner = RiskAdapter{}
var _ Scanner = DeterministicStageAdapter{}
