package validation

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

type ReferenceTuple struct {
	SourceID     domain.ID
	FieldPath    string
	Ordinal      int
	ExpectedKind domain.EntityKind
	TargetID     domain.ID
}
type FormulaTuple struct {
	SourceID          domain.ID
	FieldPath         string
	Ordinal           int
	OutputAttributeID domain.ID
	Expression        string
	Span              formula.Span
	Scopes            []string
}
type ReferenceFinding struct {
	Code  string
	Tuple ReferenceTuple
}

// WalkKnownSchema deliberately visits only the v1 envelope and payload fields.
// Entity.Extensions is never traversed, so an unknown namespace cannot become
// an implicitly validated rule.
func WalkKnownSchema(entities []domain.Entity) ([]ReferenceTuple, []FormulaTuple) {
	references, formulas := []ReferenceTuple{}, []FormulaTuple{}
	ordered := append([]domain.Entity(nil), entities...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	ordinal := 0
	for _, entity := range ordered {
		for i, id := range entity.TagIDs {
			references = append(references, ReferenceTuple{entity.ID, "/tag_ids/" + strconv.Itoa(i), ordinal, domain.KindTag, id})
			ordinal++
		}
		for key, raw := range entity.Payload {
			var value any
			if json.Unmarshal(raw, &value) != nil {
				continue
			}
			path := "/payload/" + escapePointer(key)
			if kind, ok := knownReferenceKind(key); ok {
				appendKnownReferences(entity.ID, path, value, kind, &ordinal, &references)
			}
			walkKnownPayload(entity.ID, path, value, &ordinal, &references, &formulas)
		}
	}
	sort.Slice(references, func(i, j int) bool {
		a, b := references[i], references[j]
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		if a.FieldPath != b.FieldPath {
			return a.FieldPath < b.FieldPath
		}
		return a.Ordinal < b.Ordinal
	})
	sort.Slice(formulas, func(i, j int) bool {
		a, b := formulas[i], formulas[j]
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		return a.FieldPath < b.FieldPath
	})
	return references, formulas
}
func walkKnownPayload(source domain.ID, path string, value any, ordinal *int, references *[]ReferenceTuple, formulas *[]FormulaTuple) {
	switch current := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child := current[key]
			childPath := path + "/" + escapePointer(key)
			if kind, ok := knownReferenceKind(key); ok {
				appendKnownReferences(source, childPath, child, kind, ordinal, references)
			}
			walkKnownPayload(source, childPath, child, ordinal, references, formulas)
		}
	case []any:
		for i, child := range current {
			itemPath := path + "/" + strconv.Itoa(i)
			if object, ok := child.(map[string]any); ok && (strings.HasSuffix(path, "/attribute_values") || strings.HasSuffix(path, "/costs")) {
				expression, _ := object["expression"].(string)
				output, _ := object["output_attribute_id"].(string)
				if expression != "" {
					parsed := formula.Parse(expression, nil)
					*formulas = append(*formulas, FormulaTuple{source, itemPath + "/expression", i, domain.ID(output), expression, formula.Span{StartByte: 0, EndByte: len(expression)}, selectorScopes(parsed.AST.Root)})
				}
			}
			walkKnownPayload(source, itemPath, child, ordinal, references, formulas)
		}
	}
}
func appendKnownReferences(source domain.ID, path string, value any, kind domain.EntityKind, ordinal *int, references *[]ReferenceTuple) {
	switch ids := value.(type) {
	case string:
		*references = append(*references, ReferenceTuple{source, path, *ordinal, kind, domain.ID(ids)})
		*ordinal++
	case []any:
		for i, raw := range ids {
			if id, ok := raw.(string); ok {
				*references = append(*references, ReferenceTuple{source, path + "/" + strconv.Itoa(i), *ordinal, kind, domain.ID(id)})
				*ordinal++
			}
		}
	}
}
func knownReferenceKind(key string) (domain.EntityKind, bool) {
	switch key {
	case "tag_ids", "parent_tag_ids", "enhance_tag_ids", "tag_id":
		return domain.KindTag, true
	case "skill_ids":
		return domain.KindSkill, true
	case "item_ids":
		return domain.KindItem, true
	case "effect_ids":
		return domain.KindEffect, true
	case "output_attribute_id", "attribute_id":
		return domain.KindAttribute, true
	}
	return "", false
}
func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
func selectorScopes(node formula.Node) []string {
	found := map[string]struct{}{}
	var visit func(formula.Node)
	visit = func(current formula.Node) {
		if current.Kind == formula.NodeSelector {
			found[current.Scope] = struct{}{}
		}
		for _, child := range current.Args {
			visit(child)
		}
	}
	visit(node)
	out := make([]string, 0, len(found))
	for scope := range found {
		out = append(out, scope)
	}
	sort.Strings(out)
	return out
}

func ValidateReferences(entities []domain.Entity, references []ReferenceTuple) []ReferenceFinding {
	byID := map[domain.ID]domain.Entity{}
	for _, entity := range entities {
		byID[entity.ID] = entity
	}
	findings := []ReferenceFinding{}
	for _, reference := range references {
		target, ok := byID[reference.TargetID]
		code := ""
		if !ok {
			code = "REFERENCE_NOT_FOUND"
		} else if target.Status != domain.StatusActive {
			code = "REFERENCE_TARGET_INACTIVE"
		} else if target.Kind != reference.ExpectedKind {
			code = "REFERENCE_KIND_MISMATCH"
		}
		if code != "" {
			findings = append(findings, ReferenceFinding{code, reference})
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i].Tuple, findings[j].Tuple
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		if a.FieldPath != b.FieldPath {
			return a.FieldPath < b.FieldPath
		}
		return a.Ordinal < b.Ordinal
	})
	return findings
}
