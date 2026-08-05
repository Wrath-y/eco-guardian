package validation

import "testing"

func TestEventBudgetsAndStackRules(t *testing.T) {
	loops := UnboundedEventLoops([]TriggerEdge{{"a", "b", false, false, false}, {"b", "a", false, false, false}, {"c", "c", true, false, false}})
	if len(loops) != 1 || loops[0].EvidenceHash == "" {
		t.Fatalf("%#v", loops)
	}
	if issues := ValidateStackRule(StackRuleInput{Operation: "Add"}); len(issues) != 2 || issues[0] != "STACK_PRIORITY_MISSING" || issues[1] != "STACK_UNBOUNDED" {
		t.Fatalf("%#v", issues)
	}
	priority := 1
	if issues := ValidateStackRule(StackRuleInput{Operation: "Override", Priority: &priority}); len(issues) != 0 {
		t.Fatalf("%#v", issues)
	}
}
