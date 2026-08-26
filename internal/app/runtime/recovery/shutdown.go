package recovery

import (
	"context"

	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type ShutdownOperation func(context.Context) error

type ShutdownPolicy struct {
	Kind             sharedjob.Kind
	StopClaims       ShutdownOperation
	PersistIntent    ShutdownOperation
	WaitSafeBoundary ShutdownOperation
	ProveRecoverable ShutdownOperation
}

// ShutdownCoordinator applies each boundary as a global phase. No policy can
// persist interruption while another policy still accepts claims, and owned
// dependencies are stopped by composition only after recoverability proofs.
type ShutdownCoordinator struct{ policies []ShutdownPolicy }

func NewShutdownCoordinator(policies ...ShutdownPolicy) (*ShutdownCoordinator, error) {
	seen := map[sharedjob.Kind]bool{}
	values := append([]ShutdownPolicy(nil), policies...)
	for _, policy := range values {
		if !policy.Kind.Valid() || seen[policy.Kind] || policy.StopClaims == nil || policy.PersistIntent == nil || policy.WaitSafeBoundary == nil || policy.ProveRecoverable == nil {
			return nil, ErrRegistryInvalid
		}
		seen[policy.Kind] = true
	}
	return &ShutdownCoordinator{policies: values}, nil
}

func (coordinator *ShutdownCoordinator) Prepare(ctx context.Context) error {
	if coordinator == nil {
		return ErrRegistryInvalid
	}
	for _, phase := range []func(ShutdownPolicy) ShutdownOperation{
		func(policy ShutdownPolicy) ShutdownOperation { return policy.StopClaims },
		func(policy ShutdownPolicy) ShutdownOperation { return policy.PersistIntent },
		func(policy ShutdownPolicy) ShutdownOperation { return policy.WaitSafeBoundary },
		func(policy ShutdownPolicy) ShutdownOperation { return policy.ProveRecoverable },
	} {
		for _, policy := range coordinator.policies {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := phase(policy)(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}
