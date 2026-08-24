package materialization

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/validation"
)

// Build decodes only supported v1 rule shapes from an already admitted
// immutable Source. It does not parse formula source or run safety analysis.
func Build(source Source) (RuleSetV1, error) {
	if !source.Valid() {
		return RuleSetV1{}, Diagnostic{Code: DiagnosticValidationRequired}
	}
	entities := append([]domain.Entity(nil), source.Entities...)
	sort.Slice(entities, func(i, j int) bool { return string(entities[i].ID) < string(entities[j].ID) })
	byID := make(map[domain.ID]domain.Entity, len(entities))
	for _, entity := range entities {
		if !entity.ID.Valid() || !entity.Kind.Valid() {
			return RuleSetV1{}, Diagnostic{Code: DiagnosticPayloadInvalid, SourceEntity: entity.ID}
		}
		if _, exists := byID[entity.ID]; exists {
			return RuleSetV1{}, Diagnostic{Code: DiagnosticPayloadInvalid, SourceEntity: entity.ID}
		}
		byID[entity.ID] = entity
	}
	indexes, err := formulaIndexes(source.Formulas)
	if err != nil {
		return RuleSetV1{}, err
	}
	set := RuleSetV1{ContractVersion: ContractVersionV1, ProjectID: source.ProjectID, RevisionID: source.RevisionID, ConfigHash: source.ConfigHash, Certification: source.Certification, FormulaBindings: []FormulaBinding{}, TriggerRules: []TriggerRule{}, Modifiers: []Modifier{}, StackRules: []StackRule{}}
	for _, entity := range entities {
		keys := make([]string, 0, len(entity.Payload))
		for key := range entity.Payload {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			raw := entity.Payload[key]
			path := "/payload/" + escapePointer(key)
			switch key {
			case "attribute_values", "costs":
				bindings, err := materializeBindings(source, entity, path, raw, indexes, byID)
				if err != nil {
					return RuleSetV1{}, err
				}
				set.FormulaBindings = append(set.FormulaBindings, bindings...)
			case "rule_blocks", "trigger_blocks":
				triggers, err := materializeTriggers(source, entity, path, raw, byID)
				if err != nil {
					return RuleSetV1{}, err
				}
				set.TriggerRules = append(set.TriggerRules, triggers...)
			case "attribute_modifiers", "modifiers":
				modifiers, err := materializeModifiers(source, entity, path, raw, byID)
				if err != nil {
					return RuleSetV1{}, err
				}
				set.Modifiers = append(set.Modifiers, modifiers...)
			case "stack_rule":
				stack, err := materializeStackRule(source, entity, path, raw)
				if err != nil {
					return RuleSetV1{}, err
				}
				set.StackRules = append(set.StackRules, stack)
			}
		}
	}
	sortRules(&set)
	canonical, err := canonicalRuleSet(set)
	if err != nil {
		return RuleSetV1{}, err
	}
	sum := sha256.Sum256(append([]byte(ContractVersionV1+"\x00"), canonical...))
	set.Canonical = canonical
	set.MaterializationHash = hex.EncodeToString(sum[:])
	if !set.Valid() {
		return RuleSetV1{}, ErrInvalid
	}
	return set, nil
}

type formulaIndex map[string]validation.FormulaIndexRecord

func formulaIndexes(records []validation.FormulaIndexRecord) (formulaIndex, error) {
	indexes := make(formulaIndex, len(records))
	for _, record := range records {
		if !record.SourceID.Valid() || !validPointer(record.FieldPath) || !validHash(record.ASTHash) || len(record.AST) == 0 || record.ASTVersion == "" || record.DSLVersion == "" || record.RegistryVersion == "" {
			return nil, Diagnostic{Code: DiagnosticArtifactInvalid, SourceEntity: record.SourceID, FieldPath: record.FieldPath}
		}
		key := formulaKey(record.SourceID, record.FieldPath)
		if _, exists := indexes[key]; exists {
			return nil, Diagnostic{Code: DiagnosticArtifactInvalid, SourceEntity: record.SourceID, FieldPath: record.FieldPath}
		}
		indexes[key] = record
	}
	return indexes, nil
}

func materializeBindings(source Source, entity domain.Entity, path string, raw json.RawMessage, indexes formulaIndex, byID map[domain.ID]domain.Entity) ([]FormulaBinding, error) {
	var values []struct {
		OutputAttributeID string `json:"output_attribute_id"`
		Expression        string `json:"expression"`
	}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, Diagnostic{Code: DiagnosticPayloadInvalid, SourceEntity: entity.ID, FieldPath: path}
	}
	result := make([]FormulaBinding, 0, len(values))
	for ordinal, value := range values {
		field := fmt.Sprintf("%s/%d/expression", path, ordinal)
		attributeID := domain.ID(value.OutputAttributeID)
		if value.Expression == "" || !activeKind(byID, attributeID, domain.KindAttribute) {
			return nil, Diagnostic{Code: DiagnosticReferenceInvalid, SourceEntity: entity.ID, FieldPath: field}
		}
		record, found := indexes[formulaKey(entity.ID, field)]
		if !found {
			return nil, Diagnostic{Code: DiagnosticArtifactMissing, SourceEntity: entity.ID, FieldPath: field}
		}
		if record.OutputAttributeID != "" && record.OutputAttributeID != attributeID {
			return nil, Diagnostic{Code: DiagnosticArtifactInvalid, SourceEntity: entity.ID, FieldPath: field}
		}
		astHash := sha256.Sum256(record.AST)
		if hex.EncodeToString(astHash[:]) != record.ASTHash {
			return nil, Diagnostic{Code: DiagnosticArtifactInvalid, SourceEntity: entity.ID, FieldPath: field}
		}
		bodyHash, _, err := domain.BlobHash(value)
		if err != nil {
			return nil, err
		}
		provenance, err := newProvenance(source, entity, field, ordinal, "formula_binding", bodyHash)
		if err != nil {
			return nil, err
		}
		reads := make([]FormulaRead, 0, len(record.Reads))
		for _, read := range record.Reads {
			if read.Scope == "" || read.Symbol == "" || !read.Span.Valid() {
				return nil, Diagnostic{Code: DiagnosticArtifactInvalid, SourceEntity: entity.ID, FieldPath: field}
			}
			reads = append(reads, FormulaRead{Scope: read.Scope, Symbol: read.Symbol, Start: read.Span.StartByte, End: read.Span.EndByte})
		}
		result = append(result, FormulaBinding{Provenance: provenance, OutputAttributeID: attributeID, AST: append([]byte(nil), record.AST...), ASTHash: record.ASTHash, ASTVersion: record.ASTVersion, DSLVersion: record.DSLVersion, RegistryVersion: record.RegistryVersion, Reads: reads})
	}
	return result, nil
}

type triggerPayload struct {
	Event             string         `json:"event"`
	Condition         string         `json:"condition"`
	Target            TargetSelector `json:"target"`
	EffectIDs         []string       `json:"effect_ids"`
	TerminationBudget string         `json:"termination_budget"`
}

func materializeTriggers(source Source, entity domain.Entity, path string, raw json.RawMessage, byID map[domain.ID]domain.Entity) ([]TriggerRule, error) {
	var values []triggerPayload
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, Diagnostic{Code: DiagnosticPayloadInvalid, SourceEntity: entity.ID, FieldPath: path}
	}
	result := make([]TriggerRule, 0, len(values))
	for ordinal, value := range values {
		field := fmt.Sprintf("%s/%d", path, ordinal)
		if !validEvent(value.Event) || !validTarget(value.Target, byID) || !canonicalDuration(value.TerminationBudget, true) {
			return nil, Diagnostic{Code: DiagnosticPayloadInvalid, SourceEntity: entity.ID, FieldPath: field}
		}
		effects := make([]domain.ID, len(value.EffectIDs))
		for i, rawID := range value.EffectIDs {
			effects[i] = domain.ID(rawID)
			if !activeKind(byID, effects[i], domain.KindEffect) {
				return nil, Diagnostic{Code: DiagnosticReferenceInvalid, SourceEntity: entity.ID, FieldPath: field}
			}
		}
		bodyHash, _, err := domain.BlobHash(value)
		if err != nil {
			return nil, err
		}
		provenance, err := newProvenance(source, entity, field, ordinal, "trigger_rule", bodyHash)
		if err != nil {
			return nil, err
		}
		result = append(result, TriggerRule{Provenance: provenance, Event: value.Event, Condition: value.Condition, Target: value.Target, EffectIDs: effects, TerminationBudget: value.TerminationBudget})
	}
	return result, nil
}

type modifierPayload struct {
	AttributeID string `json:"attribute_id"`
	Operation   string `json:"operation"`
	Value       string `json:"value"`
}

func materializeModifiers(source Source, entity domain.Entity, path string, raw json.RawMessage, byID map[domain.ID]domain.Entity) ([]Modifier, error) {
	var values []modifierPayload
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, Diagnostic{Code: DiagnosticPayloadInvalid, SourceEntity: entity.ID, FieldPath: path}
	}
	result := make([]Modifier, 0, len(values))
	for ordinal, value := range values {
		field := fmt.Sprintf("%s/%d", path, ordinal)
		attributeID := domain.ID(value.AttributeID)
		if !activeKind(byID, attributeID, domain.KindAttribute) {
			return nil, Diagnostic{Code: DiagnosticReferenceInvalid, SourceEntity: entity.ID, FieldPath: field}
		}
		if !validOperation(value.Operation) || !canonicalDecimal(value.Value, false) {
			return nil, Diagnostic{Code: DiagnosticPayloadInvalid, SourceEntity: entity.ID, FieldPath: field}
		}
		bodyHash, _, err := domain.BlobHash(value)
		if err != nil {
			return nil, err
		}
		provenance, err := newProvenance(source, entity, field, ordinal, "modifier", bodyHash)
		if err != nil {
			return nil, err
		}
		result = append(result, Modifier{Provenance: provenance, AttributeID: attributeID, Operation: value.Operation, Value: value.Value})
	}
	return result, nil
}

type stackPayload struct {
	Operation     string `json:"operation"`
	Priority      string `json:"priority"`
	MaxStacks     string `json:"max_stacks"`
	RefreshPolicy string `json:"refresh_policy"`
	Cap           string `json:"cap"`
}

func materializeStackRule(source Source, entity domain.Entity, path string, raw json.RawMessage) (StackRule, error) {
	var value stackPayload
	if err := json.Unmarshal(raw, &value); err != nil {
		return StackRule{}, Diagnostic{Code: DiagnosticPayloadInvalid, SourceEntity: entity.ID, FieldPath: path}
	}
	if !validOperation(value.Operation) || !validRefresh(value.RefreshPolicy) || !canonicalDuration(value.Priority, true) || !canonicalDuration(value.MaxStacks, false) || !canonicalDecimal(value.Cap, true) {
		return StackRule{}, Diagnostic{Code: DiagnosticPayloadInvalid, SourceEntity: entity.ID, FieldPath: path}
	}
	bodyHash, _, err := domain.BlobHash(value)
	if err != nil {
		return StackRule{}, err
	}
	provenance, err := newProvenance(source, entity, path, 0, "stack_rule", bodyHash)
	if err != nil {
		return StackRule{}, err
	}
	return StackRule{Provenance: provenance, Operation: value.Operation, Priority: value.Priority, MaxStacks: value.MaxStacks, RefreshPolicy: value.RefreshPolicy, Cap: value.Cap}, nil
}

func newProvenance(source Source, entity domain.Entity, path string, ordinal int, kind, bodyHash string) (Provenance, error) {
	id, err := StableRuleID(source.RevisionID, entity.ID, path, ordinal, kind)
	if err != nil {
		return Provenance{}, err
	}
	return Provenance{RuleID: id, SourceEntity: entity.ID, SourceKind: entity.Kind, FieldPath: path, Ordinal: ordinal, Kind: kind, BodyHash: bodyHash}, nil
}
func formulaKey(id domain.ID, path string) string { return string(id) + "\x00" + path }
func activeKind(all map[domain.ID]domain.Entity, id domain.ID, kind domain.EntityKind) bool {
	value, ok := all[id]
	return ok && value.Status == domain.StatusActive && value.Kind == kind
}
func validEvent(value string) bool {
	switch value {
	case "on_use", "on_hit", "on_damage", "on_heal", "on_interval", "on_effect_applied":
		return true
	}
	return false
}
func validOperation(value string) bool {
	switch value {
	case "Add", "Multiply", "Override", "Max", "Min":
		return true
	}
	return false
}
func validRefresh(value string) bool {
	switch value {
	case "refresh", "replace", "ignore":
		return true
	}
	return false
}
func validTarget(value TargetSelector, all map[domain.ID]domain.Entity) bool {
	switch value.Type {
	case "self", "source", "primary_target", "all_targets":
		return value.TagID == ""
	case "targets_with_tag":
		return activeKind(all, value.TagID, domain.KindTag)
	}
	return false
}
func canonicalDecimal(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	parsed, err := formula.ParseDecimal(value)
	return err == nil && parsed.String() == value
}
func canonicalDuration(value string, optional bool) bool {
	if value == "" {
		return optional
	}
	parsed, err := formula.ParseDuration(value)
	return err == nil && parsed.String() == value
}
func escapePointer(value string) string {
	value = stringReplace(value, "~", "~0")
	return stringReplace(value, "/", "~1")
}
func stringReplace(value, old, next string) string { return strings.ReplaceAll(value, old, next) }

func sortRules(set *RuleSetV1) {
	sort.Slice(set.FormulaBindings, func(i, j int) bool { return less(set.FormulaBindings[i].Provenance, set.FormulaBindings[j].Provenance) })
	sort.Slice(set.TriggerRules, func(i, j int) bool { return less(set.TriggerRules[i].Provenance, set.TriggerRules[j].Provenance) })
	sort.Slice(set.Modifiers, func(i, j int) bool { return less(set.Modifiers[i].Provenance, set.Modifiers[j].Provenance) })
	sort.Slice(set.StackRules, func(i, j int) bool { return less(set.StackRules[i].Provenance, set.StackRules[j].Provenance) })
}
func less(a, b Provenance) bool {
	if a.SourceEntity != b.SourceEntity {
		return string(a.SourceEntity) < string(b.SourceEntity)
	}
	if a.FieldPath != b.FieldPath {
		return a.FieldPath < b.FieldPath
	}
	if a.Ordinal != b.Ordinal {
		return a.Ordinal < b.Ordinal
	}
	return a.Kind < b.Kind
}
func canonicalRuleSet(set RuleSetV1) ([]byte, error) {
	set.Canonical, set.MaterializationHash = nil, ""
	return domain.CanonicalJSON(set)
}
