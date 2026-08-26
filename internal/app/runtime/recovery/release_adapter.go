package recovery

import (
	"context"
	"errors"

	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

var ErrProjectRecoveryBlocked = errors.New("project recovery blocks unsafe project use")

type ReleaseIntentRecoverer interface {
	Recover(context.Context) ([]versioningrelease.RecoveryResult, error)
}

// ReleaseAdapter projects the existing release saga recovery into the ordered
// runtime scan. The saga remains the only owner of intent, backup, Graph and
// active-pointer rules; this adapter has no process/health input from which it
// could infer completion.
type ReleaseAdapter struct{ Recovery ReleaseIntentRecoverer }

func (adapter ReleaseAdapter) ScanAndRecover(ctx context.Context) (ScanResult, error) {
	if adapter.Recovery == nil {
		return ScanResult{}, ErrRegistryInvalid
	}
	results, err := adapter.Recovery.Recover(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	summary := ScanResult{Scanned: len(results)}
	for _, result := range results {
		switch result.Outcome {
		case versioningrelease.RecoveryCompleted, versioningrelease.RecoveryContinued, versioningrelease.RecoveryFailed:
			summary.Recovered++
		case versioningrelease.RecoveryRequiresSupport:
			summary.RecoveryRequired++
		default:
			return ScanResult{}, ErrFactsInvalid
		}
	}
	if summary.RecoveryRequired > 0 {
		return summary, ErrProjectRecoveryBlocked
	}
	return summary, nil
}

var _ Scanner = ReleaseAdapter{}
