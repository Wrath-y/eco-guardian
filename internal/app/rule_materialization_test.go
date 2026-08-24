package app

import (
	"context"
	"errors"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/rules/materialization"
)

type ruleSourceFake struct {
	source materialization.Source
	err    error
}

func (f ruleSourceFake) ReadRuleSource(context.Context, domain.ID) (materialization.Source, error) {
	return f.source, f.err
}

func TestRuleMaterializationApplicationRejectsUnavailableSource(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	_, err = (RuleMaterializationApplication{}).MaterializeRules(context.Background(), id)
	if !errors.Is(err, ErrRuleMaterializationUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

var _ materialization.Reader = ruleSourceFake{}
