package projector

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type Formatter interface {
	Format(domain.Entity) (string, string, map[string]any, error)
}
type V1Formatter struct{}

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
	return label, strings.Join(parts, "\n"), properties, nil
}
