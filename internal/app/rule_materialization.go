package app

import (
	"context"
	"errors"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/rules/materialization"
)

var ErrRuleMaterializationUnavailable = errors.New("rule materialization is unavailable")

type ruleMaterializationReader interface {
	ReadRuleSource(context.Context, domain.ID) (materialization.Source, error)
}

// RuleMaterializationApplication is the application boundary consumed by
// deterministic engines. It intentionally exposes no working-state or raw
// entity-payload access.
type RuleMaterializationApplication struct{ Sources ruleMaterializationReader }

func (a RuleMaterializationApplication) MaterializeRules(ctx context.Context, revisionID domain.ID) (materialization.RuleSetV1, error) {
	if a.Sources == nil || !revisionID.Valid() {
		return materialization.RuleSetV1{}, ErrRuleMaterializationUnavailable
	}
	source, err := a.Sources.ReadRuleSource(ctx, revisionID)
	if err != nil {
		return materialization.RuleSetV1{}, errors.Join(ErrRuleMaterializationUnavailable, err)
	}
	set, err := materialization.Build(source)
	if err != nil {
		return materialization.RuleSetV1{}, errors.Join(ErrRuleMaterializationUnavailable, err)
	}
	return set, nil
}
