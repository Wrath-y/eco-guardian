package orchestration

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/simulation/contract"
)

var ErrFullValidationRequired = errors.New("simulation requires matching full validation")

// Admit captures a revision only after the exact FULL validation boundary has
// accepted it. Job creation deliberately belongs after this function so a
// blocked source never becomes queued durable work.
func Admit(ctx context.Context, revisions contract.RevisionSource, releases contract.ReleaseSource, gate contract.ValidationGate, selection contract.SourceSelection) (contract.Revision, error) {
	revision, err := contract.ResolveSource(ctx, revisions, releases, selection)
	if err != nil {
		return contract.Revision{}, err
	}
	if gate == nil {
		return contract.Revision{}, ErrFullValidationRequired
	}
	if err = gate.RequireFull(ctx, revision.ID); err != nil {
		return contract.Revision{}, errors.Join(ErrFullValidationRequired, err)
	}
	return revision, nil
}
