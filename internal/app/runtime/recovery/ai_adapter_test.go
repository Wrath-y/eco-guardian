package recovery

import (
	"context"
	"strings"
	"testing"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/domain"
)

type aiIdentitySourceFake struct {
	identities []aiorchestration.DeterministicRecoveryIdentity
}

func (fake aiIdentitySourceFake) ListAIRecoveryIdentities(context.Context) ([]aiorchestration.DeterministicRecoveryIdentity, error) {
	return append([]aiorchestration.DeterministicRecoveryIdentity(nil), fake.identities...), nil
}

type aiRecovererFake struct {
	calls  int
	result aiorchestration.AIRecoveryResult
	err    error
}

func (fake *aiRecovererFake) Recover(context.Context, aiorchestration.DeterministicRecoveryIdentity) (aiorchestration.AIRecoveryResult, error) {
	fake.calls++
	return fake.result, fake.err
}

func aiRecoveryIdentity() aiorchestration.DeterministicRecoveryIdentity {
	return aiorchestration.DeterministicRecoveryIdentity{JobID: domain.ID("01991a39-e000-7000-8000-000000000001"), Phase: aiorchestration.PhaseDeterministicPreview, InputHash: aicontract.Hash(strings.Repeat("a", 64)), EvidenceManifestHash: aicontract.Hash(strings.Repeat("b", 64)), DependencyHash: aicontract.Hash(strings.Repeat("c", 64))}
}

func TestAIAdapterUsesOnlyRecoveryServiceIdentityAndSurfacesRefusal(t *testing.T) {
	identity := aiRecoveryIdentity()
	recoverer := &aiRecovererFake{result: aiorchestration.AIRecoveryResult{JobID: identity.JobID, Action: aiorchestration.RecoveryInterruptedProvider}}
	adapter := AIAdapter{Identities: aiIdentitySourceFake{identities: []aiorchestration.DeterministicRecoveryIdentity{identity}}, Recovery: recoverer}
	summary, err := adapter.ScanAndRecover(context.Background())
	if err != nil || summary.Scanned != 1 || summary.Recovered != 1 || recoverer.calls != 1 {
		t.Fatalf("summary=%#v calls=%d err=%v", summary, recoverer.calls, err)
	}

	recoverer.err = aiorchestration.ErrAIRecoveryConflict
	summary, err = adapter.ScanAndRecover(context.Background())
	if err != nil || summary.RecoveryRequired != 1 || summary.Recovered != 0 {
		t.Fatalf("refusal=%#v err=%v", summary, err)
	}
}
