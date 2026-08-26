package recovery

import (
	"context"
	"errors"
	"testing"

	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

type releaseRecovererFake struct {
	results []versioningrelease.RecoveryResult
	err     error
}

func (fake releaseRecovererFake) Recover(context.Context) ([]versioningrelease.RecoveryResult, error) {
	return fake.results, fake.err
}

func TestReleaseAdapterDelegatesDurableSagaOutcomesAndBlocksUnsafeOpen(t *testing.T) {
	adapter := ReleaseAdapter{Recovery: releaseRecovererFake{results: []versioningrelease.RecoveryResult{
		{Outcome: versioningrelease.RecoveryCompleted}, {Outcome: versioningrelease.RecoveryContinued}, {Outcome: versioningrelease.RecoveryFailed},
	}}}
	summary, err := adapter.ScanAndRecover(context.Background())
	if err != nil || summary.Scanned != 3 || summary.Recovered != 3 || summary.RecoveryRequired != 0 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}

	adapter.Recovery = releaseRecovererFake{results: []versioningrelease.RecoveryResult{{Outcome: versioningrelease.RecoveryRequiresSupport}}}
	summary, err = adapter.ScanAndRecover(context.Background())
	if !errors.Is(err, ErrProjectRecoveryBlocked) || summary.RecoveryRequired != 1 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
}
