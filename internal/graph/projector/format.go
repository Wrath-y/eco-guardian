package projector

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type Formatter interface {
	Format(domain.Entity) (string, string, map[string]any, error)
}
type V1Formatter struct{}

type formatSpec struct {
	TextFields       []string
	CollectionFields []string
}

// v1FormatSpecs is the closed formatter registry. The field tables are part
// of the projection schema: payload extensions and unlisted schema fields can
// never accidentally alter search text or the Graph manifest.
var v1FormatSpecs = []struct {
	Kind domain.EntityKind
	Spec formatSpec
}{
	{domain.KindAttribute, formatSpec{TextFields: []string{"value_type", "dimension", "base_unit", "default", "min", "max", "display_scale"}}},
	{domain.KindTag, formatSpec{TextFields: []string{"category"}, CollectionFields: []string{"parent_tag_ids"}}},
	{domain.KindCharacter, formatSpec{CollectionFields: []string{"skill_ids", "item_ids"}}},
	{domain.KindSkill, formatSpec{TextFields: []string{"cooldown"}, CollectionFields: []string{"effect_ids"}}},
	{domain.KindItem, formatSpec{TextFields: []string{"slot"}, CollectionFields: []string{"effect_ids", "enhance_tag_ids"}}},
	{domain.KindEffect, formatSpec{TextFields: []string{"duration"}}},
}

// Format uses the frozen v1 envelope only. Payload and extensions remain
// schema-owned inputs to relation extraction and cannot leak unknown fields.
func (V1Formatter) Format(entity domain.Entity) (string, string, map[string]any, error) {
	if !entity.ID.Valid() || !entity.Kind.Valid() || entity.SchemaVersion < 1 {
		return "", "", nil, fmt.Errorf("invalid entity for Graph formatting")
	}
	label := entity.Name
	if label == "" {
		label = entity.Key
	}
	spec, ok := v1FormatSpec(entity.Kind)
	if !ok {
		return "", "", nil, fmt.Errorf("unregistered v1 formatter kind %q", entity.Kind)
	}
	properties := map[string]any{"key": entity.Key, "name": entity.Name, "schema_version": entity.SchemaVersion, "status": string(entity.Status)}
	if entity.Description != "" {
		properties["description"] = entity.Description
	}
	if entity.BalanceGroup != "" {
		properties["balance_group"] = entity.BalanceGroup
	}
	if len(entity.TagIDs) > 0 {
		tags := make([]string, len(entity.TagIDs))
		for i, tag := range entity.TagIDs {
			tags[i] = string(tag)
		}
		sort.Strings(tags)
		properties["tag_ids"] = tags
	}
	parts := []string{string(entity.Kind), label}
	if entity.Description != "" {
		parts = append(parts, entity.Description)
	}
	if entity.BalanceGroup != "" {
		parts = append(parts, entity.BalanceGroup)
	}
	for _, field := range spec.TextFields {
		value, text, exists, err := payloadScalar(entity.Payload, field)
		if err != nil {
			return "", "", nil, err
		}
		if exists {
			properties[field] = value
			if text != "" {
				parts = append(parts, field+": "+text)
			}
		}
	}
	for _, field := range spec.CollectionFields {
		values, exists, err := payloadStrings(entity.Payload, field)
		if err != nil {
			return "", "", nil, err
		}
		if exists {
			properties[field] = values
			if len(values) > 0 {
				parts = append(parts, field+": "+strings.Join(values, ","))
			}
		}
	}
	return label, strings.Join(parts, "\n"), properties, nil
}

func v1FormatSpec(kind domain.EntityKind) (formatSpec, bool) {
	for _, entry := range v1FormatSpecs {
		if entry.Kind == kind {
			return entry.Spec, true
		}
	}
	return formatSpec{}, false
}

func payloadScalar(payload map[string]json.RawMessage, field string) (any, string, bool, error) {
	raw, exists := payload[field]
	if !exists {
		return nil, "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, value, true, nil
	}
	var boolean bool
	if err := json.Unmarshal(raw, &boolean); err == nil {
		return boolean, fmt.Sprint(boolean), true, nil
	}
	return nil, "", false, fmt.Errorf("invalid v1 %s payload field", field)
}

func payloadStrings(payload map[string]json.RawMessage, field string) ([]string, bool, error) {
	raw, exists := payload[field]
	if !exists {
		return nil, false, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, false, fmt.Errorf("invalid v1 %s payload field: %w", field, err)
	}
	if values == nil {
		values = []string{}
	}
	values = append([]string(nil), values...)
	sort.Strings(values)
	return values, true, nil
}
