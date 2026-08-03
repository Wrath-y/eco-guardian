package domain

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type FieldIssue struct{ Path, Message string }

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var eventValues = valueSet("on_use", "on_hit", "on_damage", "on_heal", "on_interval", "on_effect_applied")
var selectorValues = valueSet("self", "source", "primary_target", "all_targets", "targets_with_tag")
var operationValues = valueSet("Add", "Multiply", "Override", "Max", "Min")

func valueSet(values ...string) map[string]struct{} {
	s := make(map[string]struct{}, len(values))
	for _, v := range values {
		s[v] = struct{}{}
	}
	return s
}

func (r *Registry) Validate(e Entity) []FieldIssue {
	issues := []FieldIssue{}
	if _, ok := r.Schema(e.Kind); !ok {
		issues = append(issues, FieldIssue{"kind", "unsupported kind"})
	}
	if !keyPattern.MatchString(e.Key) {
		issues = append(issues, FieldIssue{"key", "must be lower_snake_case"})
	}
	if strings.TrimSpace(e.Name) == "" {
		issues = append(issues, FieldIssue{"name", "is required"})
	}
	if e.Status != StatusActive && e.Status != StatusArchived {
		issues = append(issues, FieldIssue{"status", "must be active or archived"})
	}
	if e.SchemaVersion != 1 {
		issues = append(issues, FieldIssue{"schema_version", "must be 1"})
	}
	for i, id := range e.TagIDs {
		if !id.Valid() {
			issues = append(issues, FieldIssue{fmt.Sprintf("tag_ids[%d]", i), "must be UUIDv7"})
		}
	}
	for namespace := range e.Extensions {
		if !strings.Contains(namespace, "/") {
			issues = append(issues, FieldIssue{"extensions." + namespace, "must use a namespace key"})
		}
	}
	issues = append(issues, validatePayload(e.Kind, e.Payload)...)
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].Path < issues[j].Path })
	return issues
}

func validatePayload(kind EntityKind, payload map[string]json.RawMessage) []FieldIssue {
	required := map[EntityKind][]string{
		KindAttribute: {"value_type", "dimension", "base_unit", "default"}, KindTag: {"category", "parent_tag_ids"},
		KindCharacter: {"attribute_values", "skill_ids", "item_ids", "rule_blocks"}, KindSkill: {"costs", "cooldown", "target_selector", "effect_ids", "rule_blocks"},
		KindItem: {"slot", "effect_ids", "attribute_modifiers", "enhance_tag_ids", "rule_blocks"}, KindEffect: {"duration", "modifiers", "trigger_blocks", "stack_rule"},
	}
	issues := []FieldIssue{}
	for _, field := range required[kind] {
		if _, ok := payload[field]; !ok {
			issues = append(issues, FieldIssue{"payload." + field, "is required"})
		}
	}
	if raw, ok := payload["value_type"]; ok {
		var v string
		if json.Unmarshal(raw, &v) != nil || (v != "integer" && v != "decimal" && v != "boolean") {
			issues = append(issues, FieldIssue{"payload.value_type", "must be integer, decimal, or boolean"})
		}
	}
	for _, field := range []string{"parent_tag_ids", "attribute_values", "skill_ids", "item_ids", "rule_blocks", "costs", "effect_ids", "attribute_modifiers", "enhance_tag_ids", "modifiers", "trigger_blocks"} {
		if raw, ok := payload[field]; ok && !isArray(raw) {
			issues = append(issues, FieldIssue{"payload." + field, "must be an array"})
		}
	}
	var generic any
	b, _ := json.Marshal(payload)
	_ = json.Unmarshal(b, &generic)
	issues = append(issues, validateRuleValues(generic, "payload")...)
	return issues
}

func isArray(raw json.RawMessage) bool { var a []any; return json.Unmarshal(raw, &a) == nil }

func validateRuleValues(v any, path string) []FieldIssue {
	issues := []FieldIssue{}
	switch x := v.(type) {
	case map[string]any:
		for key, child := range x {
			p := path + "." + key
			if value, ok := child.(string); ok {
				var allowed map[string]struct{}
				switch key {
				case "event":
					allowed = eventValues
				case "type":
					if strings.HasSuffix(path, ".target") || strings.HasSuffix(path, ".target_selector") || x["tag_id"] != nil {
						allowed = selectorValues
					}
				case "operation":
					allowed = operationValues
				}
				if allowed != nil {
					if _, ok := allowed[value]; !ok {
						issues = append(issues, FieldIssue{p, "unregistered value"})
					}
				}
			}
			issues = append(issues, validateRuleValues(child, p)...)
		}
	case []any:
		for i, child := range x {
			issues = append(issues, validateRuleValues(child, fmt.Sprintf("%s[%d]", path, i))...)
		}
	}
	return issues
}
