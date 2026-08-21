package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// Template preserves the canonical immutable source body for a built-in
// scenario. Built-ins are never returned by reference or mutated in place.
type Template struct {
	Definition Definition
	Origin     string
	Body       []byte
	BodyHash   string
}

// Registry resolves only registered immutable scenario versions and checks
// their event/evaluator references before an application can use them.
type Registry struct{ templates map[string]Template }

func NewRegistry(templates []Template, eventIDs, evaluatorIDs []string) (*Registry, error) {
	events, err := identitySet(eventIDs)
	if err != nil {
		return nil, fmt.Errorf("invalid event registry: %w", err)
	}
	evaluators, err := identitySet(evaluatorIDs)
	if err != nil {
		return nil, fmt.Errorf("invalid evaluator registry: %w", err)
	}
	registry := &Registry{templates: make(map[string]Template, len(templates))}
	for _, template := range templates {
		if template.Origin != "builtin" || template.Definition.Validate() != nil {
			return nil, fmt.Errorf("invalid scenario template %q", template.Definition.ID)
		}
		body, err := json.Marshal(template.Definition)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(body)
		if string(template.Body) != string(body) || template.BodyHash != hex.EncodeToString(hash[:]) {
			return nil, fmt.Errorf("scenario template %q body hash mismatch", template.Definition.ID)
		}
		for _, action := range template.Definition.Actions {
			if _, found := events[action.EventID]; !found {
				return nil, fmt.Errorf("scenario template %q references unregistered event %q", template.Definition.ID, action.EventID)
			}
			if _, found := evaluators[action.EvaluatorID]; !found {
				return nil, fmt.Errorf("scenario template %q references unregistered evaluator %q", template.Definition.ID, action.EvaluatorID)
			}
		}
		key := template.Definition.ID + "@" + template.Definition.Version
		if _, duplicate := registry.templates[key]; duplicate {
			return nil, fmt.Errorf("duplicate scenario template %q", key)
		}
		registry.templates[key] = cloneTemplate(template)
	}
	return registry, nil
}

func (r *Registry) Get(id, version string) (Template, bool) {
	template, found := r.templates[id+"@"+version]
	return cloneTemplate(template), found
}

func (r *Registry) Templates() []Template {
	items := make([]Template, 0, len(r.templates))
	for _, template := range r.templates {
		items = append(items, cloneTemplate(template))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Definition.ID < items[j].Definition.ID })
	return items
}

func identitySet(ids []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if !stableID(id) {
			return nil, fmt.Errorf("invalid identity %q", id)
		}
		if _, duplicate := result[id]; duplicate {
			return nil, fmt.Errorf("duplicate identity %q", id)
		}
		result[id] = struct{}{}
	}
	return result, nil
}

func cloneTemplate(template Template) Template {
	definition, err := ParseDefinition(template.Body)
	if err == nil {
		template.Definition = definition
	}
	template.Body = append([]byte(nil), template.Body...)
	return template
}
