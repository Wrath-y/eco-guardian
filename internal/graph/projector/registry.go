package projector

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
)

type ProjectionSchemaVersion string
type ProjectorVersion string

const (
	ProjectionSchemaV1 ProjectionSchemaVersion = "1"
	ProjectorV1        ProjectorVersion        = "v1"
)

var ErrProjectorVersionUnavailable = errors.New("PROJECTOR_VERSION_UNAVAILABLE")

type RelationToken string

const (
	CharacterHasSkill       RelationToken = "character_has_skill"
	CharacterUsesItem       RelationToken = "character_uses_item"
	SkillAppliesEffect      RelationToken = "skill_applies_effect"
	ItemAppliesEffect       RelationToken = "item_applies_effect"
	FormulaReadsAttribute   RelationToken = "formula_reads_attribute"
	EffectModifiesAttribute RelationToken = "effect_modifies_attribute"
	EntityHasTag            RelationToken = "entity_has_tag"
	ItemEnhancesTag         RelationToken = "item_enhances_tag"
	EffectTriggersEffect    RelationToken = "effect_triggers_effect"
)

type RelationDescriptor struct {
	Token             RelationToken
	SourceKinds       []domain.EntityKind
	TargetKind        domain.EntityKind
	PathPattern       string
	Direction         string
	OccurrenceOrder   string
	ProvenanceBuilder string
}

func (d RelationDescriptor) Valid() bool {
	if d.Token == "" || !d.TargetKind.Valid() || !strings.HasPrefix(d.PathPattern, "/") || len(d.SourceKinds) == 0 || d.Direction != "source_to_target" || d.OccurrenceOrder != "source_field_order" || d.ProvenanceBuilder != "v1" {
		return false
	}
	for _, kind := range d.SourceKinds {
		if !kind.Valid() {
			return false
		}
	}
	return true
}

// V1Relations is the closed, source-to-target relation vocabulary. `*` only
// matches a decimal array index; it is not a general JSONPath expression.
func V1Relations() []RelationDescriptor {
	return []RelationDescriptor{
		{CharacterHasSkill, []domain.EntityKind{domain.KindCharacter}, domain.KindSkill, "/payload/skill_ids/*", "source_to_target", "source_field_order", "v1"},
		{CharacterUsesItem, []domain.EntityKind{domain.KindCharacter}, domain.KindItem, "/payload/item_ids/*", "source_to_target", "source_field_order", "v1"},
		{SkillAppliesEffect, []domain.EntityKind{domain.KindSkill}, domain.KindEffect, "/payload/effect_ids/*", "source_to_target", "source_field_order", "v1"},
		{ItemAppliesEffect, []domain.EntityKind{domain.KindItem}, domain.KindEffect, "/payload/effect_ids/*", "source_to_target", "source_field_order", "v1"},
		{FormulaReadsAttribute, []domain.EntityKind{domain.KindCharacter, domain.KindSkill}, domain.KindAttribute, "/payload/*/*/expression", "source_to_target", "source_field_order", "v1"},
		{EffectModifiesAttribute, []domain.EntityKind{domain.KindEffect}, domain.KindAttribute, "/payload/modifiers/*/attribute_id", "source_to_target", "source_field_order", "v1"},
		{EntityHasTag, []domain.EntityKind{domain.KindAttribute, domain.KindTag, domain.KindCharacter, domain.KindSkill, domain.KindItem, domain.KindEffect}, domain.KindTag, "/tag_ids/*", "source_to_target", "source_field_order", "v1"},
		{ItemEnhancesTag, []domain.EntityKind{domain.KindItem}, domain.KindTag, "/payload/enhance_tag_ids/*", "source_to_target", "source_field_order", "v1"},
		{EffectTriggersEffect, []domain.EntityKind{domain.KindEffect}, domain.KindEffect, "/payload/trigger_blocks/*/effect_ids/*", "source_to_target", "source_field_order", "v1"},
	}
}

type Descriptor struct {
	SchemaVersion ProjectionSchemaVersion
	Version       ProjectorVersion
	Relations     []RelationDescriptor
	Formatter     Formatter
}

func (d Descriptor) Valid() bool {
	if d.SchemaVersion == "" || d.Version == "" || !validRelations(d.Relations) || d.Formatter == nil {
		return false
	}
	return d.SchemaVersion != ProjectionSchemaV1 || reflect.DeepEqual(d.Relations, V1Relations())
}
func validRelations(relations []RelationDescriptor) bool {
	if len(relations) != 9 {
		return false
	}
	seen := map[RelationToken]struct{}{}
	for _, relation := range relations {
		if !relation.Valid() {
			return false
		}
		if _, exists := seen[relation.Token]; exists {
			return false
		}
		seen[relation.Token] = struct{}{}
	}
	return true
}

type registryKey struct {
	Schema  ProjectionSchemaVersion
	Version ProjectorVersion
}
type Registry struct {
	descriptors map[registryKey]Descriptor
	defaultKey  registryKey
}

func NewRegistry(defaultSchema ProjectionSchemaVersion, defaultVersion ProjectorVersion, descriptors ...Descriptor) (*Registry, error) {
	registry := &Registry{descriptors: make(map[registryKey]Descriptor, len(descriptors)), defaultKey: registryKey{defaultSchema, defaultVersion}}
	for _, descriptor := range descriptors {
		if !descriptor.Valid() {
			return nil, fmt.Errorf("invalid projector descriptor")
		}
		key := registryKey{descriptor.SchemaVersion, descriptor.Version}
		if _, exists := registry.descriptors[key]; exists {
			return nil, fmt.Errorf("duplicate projector identity %s/%s", key.Schema, key.Version)
		}
		registry.descriptors[key] = cloneDescriptor(descriptor)
	}
	if _, exists := registry.descriptors[registry.defaultKey]; !exists {
		return nil, fmt.Errorf("default projector is not registered")
	}
	return registry, nil
}
func (r *Registry) Resolve(schema ProjectionSchemaVersion, version ProjectorVersion) (Descriptor, error) {
	if r == nil {
		return Descriptor{}, ErrProjectorVersionUnavailable
	}
	descriptor, exists := r.descriptors[registryKey{schema, version}]
	if !exists {
		return Descriptor{}, fmt.Errorf("%w: %s/%s", ErrProjectorVersionUnavailable, schema, version)
	}
	return cloneDescriptor(descriptor), nil
}
func (r *Registry) Default() Descriptor {
	if r == nil {
		return Descriptor{}
	}
	return cloneDescriptor(r.descriptors[r.defaultKey])
}
func cloneDescriptor(descriptor Descriptor) Descriptor {
	copy := descriptor
	copy.Relations = append([]RelationDescriptor(nil), descriptor.Relations...)
	for index := range copy.Relations {
		copy.Relations[index].SourceKinds = append([]domain.EntityKind(nil), descriptor.Relations[index].SourceKinds...)
	}
	return copy
}
func RelationTokens(relations []RelationDescriptor) []RelationToken {
	tokens := make([]RelationToken, 0, len(relations))
	for _, relation := range relations {
		tokens = append(tokens, relation.Token)
	}
	sort.Slice(tokens, func(i, j int) bool { return tokens[i] < tokens[j] })
	return tokens
}
