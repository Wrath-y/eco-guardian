package validation

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type TriggerRuleRecord struct {
	EntityID  domain.ID
	FieldPath string
	Edge      TriggerEdge
}
type StackRuleRecord struct {
	EntityID  domain.ID
	FieldPath string
	Rule      StackRuleInput
}

func ExtractRuleSafety(entities []domain.Entity) ([]TriggerRuleRecord, []StackRuleRecord) {
	triggers := []TriggerRuleRecord{}
	stacks := []StackRuleRecord{}
	for _, entity := range entities {
		for key, raw := range entity.Payload {
			var value any
			if json.Unmarshal(raw, &value) == nil {
				walkRules(entity.ID, "/payload/"+escapePointer(key), value, &triggers, &stacks)
			}
		}
	}
	return triggers, stacks
}
func walkRules(entityID domain.ID, path string, value any, triggers *[]TriggerRuleRecord, stacks *[]StackRuleRecord) {
	switch current := value.(type) {
	case map[string]any:
		if strings.HasSuffix(path, "/stack_rule") {
			*stacks = append(*stacks, StackRuleRecord{entityID, path, stackRule(current)})
		}
		if effects, ok := current["effect_ids"].([]any); ok {
			bounded := positive(current["termination_budget"])
			for _, effect := range effects {
				if target, ok := effect.(string); ok {
					*triggers = append(*triggers, TriggerRuleRecord{entityID, path, TriggerEdge{From: string(entityID) + path, To: target, HasPositiveCount: bounded}})
				}
			}
		}
		for key, child := range current {
			walkRules(entityID, path+"/"+escapePointer(key), child, triggers, stacks)
		}
	case []any:
		for index, child := range current {
			walkRules(entityID, path+"/"+strconv.Itoa(index), child, triggers, stacks)
		}
	}
}
func positive(value any) bool {
	switch current := value.(type) {
	case string:
		number, err := strconv.ParseInt(current, 10, 64)
		return err == nil && number > 0
	case float64:
		return current > 0
	}
	return false
}
func stackRule(value map[string]any) StackRuleInput {
	rule := StackRuleInput{}
	rule.Operation, _ = value["operation"].(string)
	if raw, ok := value["priority"]; ok {
		if number, ok := asInt(raw); ok {
			rule.Priority = &number
		}
	}
	if raw, ok := value["max_stacks"]; ok {
		if number, ok := asInt(raw); ok {
			rule.MaxStacks = &number
		}
	}
	rule.HasPositiveCap = positive(value["cap"])
	return rule
}
func asInt(value any) (int, bool) {
	switch current := value.(type) {
	case string:
		number, err := strconv.Atoi(current)
		return number, err == nil
	case float64:
		return int(current), current == float64(int(current))
	}
	return 0, false
}
