package validation

import (
	"encoding/json"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestExtractRuleSafetyUsesCanonicalNumericStrings(t *testing.T) {
	id := referenceID(t)
	target := referenceID(t)
	entity := domain.Entity{ID: id, Payload: map[string]json.RawMessage{"rule_blocks": json.RawMessage(`[{"effect_ids":["` + string(target) + `"],"termination_budget":"1"}]`), "stack_rule": json.RawMessage(`{"operation":"Add","priority":"1","max_stacks":"2"}`)}}
	triggers, stacks := ExtractRuleSafety([]domain.Entity{entity})
	if len(triggers) != 1 || !triggers[0].Edge.HasPositiveCount {
		t.Fatalf("%#v", triggers)
	}
	if len(stacks) != 1 || len(ValidateStackRule(stacks[0].Rule)) != 0 {
		t.Fatalf("%#v", stacks)
	}
}
