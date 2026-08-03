package domain

import (
	"encoding/json"
	"fmt"
	"sort"
)

type Reference struct {
	SourceID     ID         `json:"source_id"`
	FieldPath    string     `json:"field_path"`
	ExpectedKind EntityKind `json:"expected_kind"`
	TargetID     ID         `json:"target_id"`
	Ordinal      int        `json:"ordinal"`
}

type TagIndex struct{ EntityID, TagID ID }

func ExtractIndexes(entity Entity) ([]Reference, []TagIndex) {
	refs := []Reference{}
	tags := []TagIndex{}
	ordinal := 0
	add := func(path string, kind EntityKind, id ID) {
		if id.Valid() {
			refs = append(refs, Reference{SourceID: entity.ID, FieldPath: path, ExpectedKind: kind, TargetID: id, Ordinal: ordinal})
			ordinal++
		}
	}
	for i, id := range entity.TagIDs {
		add(fmt.Sprintf("tag_ids[%d]", i), KindTag, id)
		if id.Valid() {
			tags = append(tags, TagIndex{entity.ID, id})
		}
	}
	var payload any
	b, _ := json.Marshal(entity.Payload)
	_ = json.Unmarshal(b, &payload)
	walkReferences(payload, "payload", add)
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].FieldPath == refs[j].FieldPath {
			return refs[i].Ordinal < refs[j].Ordinal
		}
		return refs[i].FieldPath < refs[j].FieldPath
	})
	return refs, tags
}

func walkReferences(value any, path string, add func(string, EntityKind, ID)) {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child, childPath := v[key], path+"."+key
			if kind, ok := referenceFieldKind(key); ok {
				switch ids := child.(type) {
				case string:
					add(childPath, kind, ID(ids))
				case []any:
					for i, item := range ids {
						if id, ok := item.(string); ok {
							add(fmt.Sprintf("%s[%d]", childPath, i), kind, ID(id))
						}
					}
				}
			}
			walkReferences(child, childPath, add)
		}
	case []any:
		for i, child := range v {
			walkReferences(child, fmt.Sprintf("%s[%d]", path, i), add)
		}
	}
}

func referenceFieldKind(field string) (EntityKind, bool) {
	switch field {
	case "tag_ids", "parent_tag_ids", "enhance_tag_ids", "tag_id":
		return KindTag, true
	case "skill_ids":
		return KindSkill, true
	case "item_ids":
		return KindItem, true
	case "effect_ids":
		return KindEffect, true
	case "output_attribute_id", "attribute_id":
		return KindAttribute, true
	default:
		return "", false
	}
}
