package recovery

import (
	"context"
	"errors"

	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
)

type AIRecoveryIdentitySource interface {
	ListAIRecoveryIdentities(context.Context) ([]aiorchestration.DeterministicRecoveryIdentity, error)
}

type AIRecoverer interface {
	Recover(context.Context, aiorchestration.DeterministicRecoveryIdentity) (aiorchestration.AIRecoveryResult, error)
}

// AIAdapter invokes only AIRecoveryService, whose port set deliberately has no
// Provider/model invocation. Deterministic checkpoints may be reused, while a
// possibly in-flight Provider attempt is durably interrupted by that service.
type AIAdapter struct {
	Identities AIRecoveryIdentitySource
	Recovery   AIRecoverer
}

func (adapter AIAdapter) ScanAndRecover(ctx context.Context) (ScanResult, error) {
	if adapter.Identities == nil || adapter.Recovery == nil {
		return ScanResult{}, ErrRegistryInvalid
	}
	identities, err := adapter.Identities.ListAIRecoveryIdentities(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	summary := ScanResult{Scanned: len(identities)}
	for _, identity := range identities {
		if !identity.Valid() {
			summary.RecoveryRequired++
			continue
		}
		result, recoverErr := adapter.Recovery.Recover(ctx, identity)
		if errors.Is(recoverErr, aiorchestration.ErrAIRecoveryInvalid) || errors.Is(recoverErr, aiorchestration.ErrAIRecoveryConflict) {
			summary.RecoveryRequired++
			continue
		}
		if recoverErr != nil {
			return summary, recoverErr
		}
		if result.JobID != identity.JobID {
			return summary, ErrFactsInvalid
		}
		summary.Recovered++
	}
	return summary, nil
}

var _ Scanner = AIAdapter{}
var _ AIRecoverer = aiorchestration.AIRecoveryService{}
