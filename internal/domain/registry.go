package domain

import (
	"embed"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed assets/*.schema.json
var schemaFiles embed.FS

type Schema struct {
	ID  string
	Raw json.RawMessage
}

type Registry struct {
	byID   map[string]Schema
	byKind map[EntityKind]string
}

var kindSchema = map[EntityKind]string{
	KindAttribute: "urn:eco:schema:attribute:1", KindTag: "urn:eco:schema:tag:1",
	KindCharacter: "urn:eco:schema:character:1", KindSkill: "urn:eco:schema:skill:1",
	KindItem: "urn:eco:schema:item:1", KindEffect: "urn:eco:schema:effect:1",
}

func NewRegistry() (*Registry, error) {
	entries, err := schemaFiles.ReadDir("assets")
	if err != nil {
		return nil, err
	}
	r := &Registry{byID: map[string]Schema{}, byKind: map[EntityKind]string{}}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		raw, err := schemaFiles.ReadFile("assets/" + entry.Name())
		if err != nil {
			return nil, err
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("parse %s: %w", entry.Name(), err)
		}
		id, _ := doc["$id"].(string)
		if id == "" {
			return nil, fmt.Errorf("schema %s has no $id", entry.Name())
		}
		if _, exists := r.byID[id]; exists {
			return nil, fmt.Errorf("duplicate schema id %q", id)
		}
		r.byID[id] = Schema{ID: id, Raw: append(json.RawMessage(nil), raw...)}
	}
	for kind, id := range kindSchema {
		if _, ok := r.byID[id]; !ok {
			return nil, fmt.Errorf("kind %s points to missing schema %s", kind, id)
		}
		r.byKind[kind] = id
	}
	if len(r.byKind) != 6 {
		return nil, fmt.Errorf("expected exactly six built-in kinds")
	}
	for id, schema := range r.byID {
		for _, ref := range schemaReferences(schema.Raw) {
			if strings.HasPrefix(ref, "urn:eco:schema:") {
				if _, ok := r.byID[ref]; !ok {
					return nil, fmt.Errorf("schema %s references missing %s", id, ref)
				}
			}
		}
	}
	return r, nil
}

func (r *Registry) Schema(kind EntityKind) (Schema, bool) {
	id, ok := r.byKind[kind]
	if !ok {
		return Schema{}, false
	}
	s, ok := r.byID[id]
	return s, ok
}

func (r *Registry) Kinds() []EntityKind {
	kinds := make([]EntityKind, 0, len(r.byKind))
	for k := range r.byKind {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}

func schemaReferences(raw []byte) []string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	var refs []string
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["$ref"].(string); ok {
				refs = append(refs, ref)
			}
			for _, child := range x {
				visit(child)
			}
		case []any:
			for _, child := range x {
				visit(child)
			}
		}
	}
	visit(value)
	return refs
}
