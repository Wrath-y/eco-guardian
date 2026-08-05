package domain

import (
	"encoding/json"
	"fmt"

	"github.com/zouyi/eco-guardian/internal/formula"
)

// NormalizeNumericEntity is the one persistence-boundary conversion for
// numeric payload values. It accepts user-entered decimal text but never an
// IEEE-754 JSON number, so canonical entity blobs and revision hashes have a
// single stable representation.
func NormalizeNumericEntity(entity Entity) (Entity, error) {
	copy := entity
	copy.Payload = make(map[string]json.RawMessage, len(entity.Payload))
	for key, raw := range entity.Payload {
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return Entity{}, fmt.Errorf("payload.%s: %w", key, err)
		}
		normalized, err := normalizeNumericValue(value, key)
		if err != nil {
			return Entity{}, err
		}
		encoded, err := json.Marshal(normalized)
		if err != nil {
			return Entity{}, err
		}
		copy.Payload[key] = encoded
	}
	return copy, nil
}

func normalizeNumericValue(value any, key string) (any, error) {
	switch current := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(current))
		for name, child := range current {
			normalized, err := normalizeNumericValue(child, name)
			if err != nil {
				return nil, err
			}
			out[name] = normalized
		}
		return out, nil
	case []any:
		out := make([]any, len(current))
		for i, child := range current {
			normalized, err := normalizeNumericValue(child, key)
			if err != nil {
				return nil, err
			}
			out[i] = normalized
		}
		return out, nil
	case string:
		switch key {
		case "duration", "cooldown", "termination_budget", "priority", "max_stacks":
			duration, err := formula.ParseDuration(current)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			if (key == "duration" || key == "cooldown" || key == "termination_budget" || key == "max_stacks") && duration < 0 {
				return nil, fmt.Errorf("%s must not be negative", key)
			}
			return duration.String(), nil
		case "default", "min", "max", "display_scale", "value", "cap":
			decimal, err := formula.ParseDecimal(current)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			return decimal.String(), nil
		}
		return current, nil
	case float64:
		if numericPayloadField(key) {
			return nil, fmt.Errorf("%s must be a canonical numeric string", key)
		}
		return current, nil
	default:
		return current, nil
	}
}

func numericPayloadField(key string) bool {
	switch key {
	case "duration", "cooldown", "termination_budget", "priority", "max_stacks", "default", "min", "max", "display_scale", "value", "cap":
		return true
	}
	return false
}
