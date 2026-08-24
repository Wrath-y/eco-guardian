package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
	"github.com/zouyi/eco-guardian/internal/rules/materialization"
)

var (
	ErrTypedRuleAdapterInvalid = errors.New("simulation typed rule adapter is invalid")
	ErrTypedRuleReference      = errors.New("simulation typed rule reference is missing")
)

// RuleContext is the immutable per-event view passed to a materialized rule.
// Entity references are revision identities, not display names or database row
// ids.  The evaluator only reads this snapshot; state mutation is returned as
// an explicit value for the registered event adapter to apply.
type RuleContext struct {
	SelfID   domain.ID
	SourceID domain.ID
	TargetID domain.ID
	Entities map[domain.ID]RuleEntity
	Scenario formula.RuntimeTable
	Event    Event
}

type RuleEntity struct {
	Attributes map[string]formula.Result
	Tags       map[domain.ID]struct{}
}

// BoundValue is a typed FormulaBinding output. The caller decides where this
// immutable output belongs in its SimulationState; no string expression or
// untyped value crosses the engine boundary.
type BoundValue struct {
	RuleID      string
	AttributeID domain.ID
	Value       formula.Result
}

// StackInstruction is the typed form of a StackRule. Count and priority keep
// their #6 integer-millisecond representation; Cap remains a decimal128.
type StackInstruction struct {
	RuleID        string
	SourceEntity  domain.ID
	Operation     string
	PriorityMS    *formula.Duration
	MaxStacks     formula.Duration
	RefreshPolicy string
	Cap           *formula.Decimal
}

// EffectInvocation is the structured output of a TriggerRule. Effect IDs are
// still immutable revision identities and are resolved only by the registered
// event/evaluator map supplied to the adapter.
type EffectInvocation struct {
	RuleID       string
	EffectID     domain.ID
	TargetID     domain.ID
	EventID      string
	EventVersion string
	EvaluatorID  string
}

// EventRegistration freezes the bridge between an immutable effect identity
// and an engine event/evaluator identity. It replaces ad-hoc string dispatch.
type EventRegistration struct {
	EffectID     domain.ID
	EventID      string
	EventVersion string
	EvaluatorID  string
}

// TypedRuleAdapter is a versioned, data-only adapter over a FULL-certified
// RuleSetV1. It never reads a working revision or reparses formula source.
type TypedRuleAdapter struct {
	rules         materialization.RuleSetV1
	registry      *formula.Registry
	registrations map[domain.ID]EventRegistration
}

func NewTypedRuleAdapter(rules materialization.RuleSetV1, registry *formula.Registry, registrations []EventRegistration) (*TypedRuleAdapter, error) {
	if registry == nil || materialization.Verify(rules) != nil {
		return nil, ErrTypedRuleAdapterInvalid
	}
	adapter := &TypedRuleAdapter{rules: rules, registry: registry, registrations: make(map[domain.ID]EventRegistration, len(registrations))}
	for _, registration := range registrations {
		if !registration.EffectID.Valid() || !stableID(registration.EventID) || !stableID(registration.EventVersion) || !stableID(registration.EvaluatorID) {
			return nil, ErrTypedRuleAdapterInvalid
		}
		if _, exists := adapter.registrations[registration.EffectID]; exists {
			return nil, ErrTypedRuleAdapterInvalid
		}
		adapter.registrations[registration.EffectID] = registration
	}
	for _, trigger := range rules.TriggerRules {
		if trigger.Condition != "" {
			// #5 v1 preserves condition as an opaque stable reference. It is not
			// a typed AST, so executing it here would require an impermissible
			// reparse or dynamic evaluator.
			return nil, ErrTypedRuleAdapterInvalid
		}
		for _, effectID := range trigger.EffectIDs {
			if _, exists := adapter.registrations[effectID]; !exists {
				return nil, ErrTypedRuleAdapterInvalid
			}
		}
	}
	for _, binding := range rules.FormulaBindings {
		if err := adapter.verifyAST(binding); err != nil {
			return nil, err
		}
	}
	return adapter, nil
}

func (adapter *TypedRuleAdapter) EvaluateFormulas(context RuleContext) ([]BoundValue, error) {
	if adapter == nil || !context.SelfID.Valid() || !context.SourceID.Valid() || !context.TargetID.Valid() {
		return nil, ErrTypedRuleAdapterInvalid
	}
	values := make([]BoundValue, 0, len(adapter.rules.FormulaBindings))
	for _, binding := range adapter.rules.FormulaBindings {
		ast, err := adapter.decodeAST(binding)
		if err != nil {
			return nil, err
		}
		runtime, err := runtimeSymbols(binding, context)
		if err != nil {
			return nil, err
		}
		value, diagnostics := formula.EvaluateWithSymbols(ast.Root, adapter.registry, runtime)
		if len(diagnostics) != 0 {
			return nil, EvaluatorDiagnostic(diagnostics[0].Code)
		}
		values = append(values, BoundValue{RuleID: binding.RuleID, AttributeID: binding.OutputAttributeID, Value: value})
	}
	return values, nil
}

// TriggeredEffects resolves only matching, unconditional v1 triggers. Target
// selection is performed over the supplied immutable context and the returned
// actions retain the registered event/evaluator identities.
func (adapter *TypedRuleAdapter) TriggeredEffects(context RuleContext, triggerEvent string) ([]EffectInvocation, error) {
	if adapter == nil || !context.SelfID.Valid() || !context.SourceID.Valid() || !context.TargetID.Valid() || triggerEvent == "" {
		return nil, ErrTypedRuleAdapterInvalid
	}
	result := []EffectInvocation{}
	for _, rule := range adapter.rules.TriggerRules {
		if rule.Event != triggerEvent || rule.Condition != "" {
			continue
		}
		targets, err := selectTargets(rule.Target, context)
		if err != nil {
			return nil, err
		}
		for _, targetID := range targets {
			for _, effectID := range rule.EffectIDs {
				registration, exists := adapter.registrations[effectID]
				if !exists {
					return nil, ErrTypedRuleReference
				}
				result = append(result, EffectInvocation{RuleID: rule.RuleID, EffectID: effectID, TargetID: targetID, EventID: registration.EventID, EventVersion: registration.EventVersion, EvaluatorID: registration.EvaluatorID})
			}
		}
	}
	return result, nil
}

// ApplyModifiers maps materialized modifiers to ordered, typed transitions for
// one target. It intentionally returns values instead of mutating an engine
// map, so the registered evaluator remains responsible for state ownership and
// event observation ordering.
func (adapter *TypedRuleAdapter) ApplyModifiers(target RuleEntity) ([]BoundValue, error) {
	if adapter == nil {
		return nil, ErrTypedRuleAdapterInvalid
	}
	attributes := make(map[string]formula.Result, len(target.Attributes))
	for key, value := range target.Attributes {
		attributes[key] = value
	}
	result := make([]BoundValue, 0, len(adapter.rules.Modifiers))
	for _, modifier := range adapter.rules.Modifiers {
		key := string(modifier.AttributeID)
		current, found := attributes[key]
		if !found || current.Decimal == nil || current.Boolean != nil {
			return nil, ErrTypedRuleReference
		}
		value, err := modifierValue(modifier, current.Type, adapter.registry)
		if err != nil {
			return nil, err
		}
		next, err := applyModifier(modifier.Operation, current, value, adapter.registry)
		if err != nil {
			return nil, err
		}
		attributes[key] = next
		result = append(result, BoundValue{RuleID: modifier.RuleID, AttributeID: modifier.AttributeID, Value: next})
	}
	return result, nil
}

// StackInstructions turns validated StackRules into explicit typed inputs for
// a registered stack evaluator. No generic script or runtime code is invoked.
func (adapter *TypedRuleAdapter) StackInstructions() ([]StackInstruction, error) {
	if adapter == nil {
		return nil, ErrTypedRuleAdapterInvalid
	}
	result := make([]StackInstruction, 0, len(adapter.rules.StackRules))
	for _, rule := range adapter.rules.StackRules {
		maxStacks, err := formula.ParseDuration(rule.MaxStacks)
		if err != nil || maxStacks < 0 {
			return nil, ErrTypedRuleAdapterInvalid
		}
		instruction := StackInstruction{RuleID: rule.RuleID, SourceEntity: rule.SourceEntity, Operation: rule.Operation, MaxStacks: maxStacks, RefreshPolicy: rule.RefreshPolicy}
		if rule.Priority != "" {
			priority, err := formula.ParseDuration(rule.Priority)
			if err != nil {
				return nil, ErrTypedRuleAdapterInvalid
			}
			instruction.PriorityMS = &priority
		}
		if rule.Cap != "" {
			cap, err := formula.ParseDecimal(rule.Cap)
			if err != nil {
				return nil, ErrTypedRuleAdapterInvalid
			}
			instruction.Cap = &cap
		}
		result = append(result, instruction)
	}
	return result, nil
}

func (adapter *TypedRuleAdapter) verifyAST(binding materialization.FormulaBinding) error {
	_, err := adapter.decodeAST(binding)
	return err
}

func (adapter *TypedRuleAdapter) decodeAST(binding materialization.FormulaBinding) (formula.AST, error) {
	if binding.ASTVersion != formula.ASTSchemaVersion || binding.DSLVersion != formula.DSLVersion {
		return formula.AST{}, ErrTypedRuleAdapterInvalid
	}
	sum := sha256.Sum256(binding.AST)
	if hex.EncodeToString(sum[:]) != binding.ASTHash {
		return formula.AST{}, ErrTypedRuleAdapterInvalid
	}
	var ast formula.AST
	if err := json.Unmarshal(binding.AST, &ast); err != nil || ast.Version != formula.ASTSchemaVersion {
		return formula.AST{}, ErrTypedRuleAdapterInvalid
	}
	canonical, err := ast.CanonicalBytes()
	if err != nil || string(canonical) != string(binding.AST) {
		return formula.AST{}, ErrTypedRuleAdapterInvalid
	}
	return ast, nil
}

func runtimeSymbols(binding materialization.FormulaBinding, context RuleContext) (formula.RuntimeTable, error) {
	runtime := formula.RuntimeTable{}
	for _, read := range binding.Reads {
		var entityID domain.ID
		switch read.Scope {
		case "self":
			entityID = context.SelfID
		case "source":
			entityID = context.SourceID
		case "target":
			entityID = context.TargetID
		case "scenario":
			value, found := context.Scenario.ResolveValue(read.Scope, read.Symbol)
			if !found {
				return nil, ErrTypedRuleReference
			}
			runtime[read.Scope+":"+read.Symbol] = value
			continue
		default:
			return nil, ErrTypedRuleReference
		}
		entity, found := context.Entities[entityID]
		if !found {
			return nil, ErrTypedRuleReference
		}
		value, found := entity.Attributes[read.Symbol]
		if !found {
			return nil, ErrTypedRuleReference
		}
		runtime[read.Scope+":"+read.Symbol] = value
	}
	return runtime, nil
}

func selectTargets(selector materialization.TargetSelector, context RuleContext) ([]domain.ID, error) {
	switch selector.Type {
	case "self":
		return []domain.ID{context.SelfID}, nil
	case "source":
		return []domain.ID{context.SourceID}, nil
	case "primary_target":
		return []domain.ID{context.TargetID}, nil
	case "all_targets":
		ids := make([]domain.ID, 0, len(context.Entities))
		for id := range context.Entities {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
		return ids, nil
	case "targets_with_tag":
		ids := []domain.ID{}
		for id, entity := range context.Entities {
			if _, found := entity.Tags[selector.TagID]; found {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(i, j int) bool { return string(ids[i]) < string(ids[j]) })
		return ids, nil
	default:
		return nil, fmt.Errorf("%w: target selector", ErrTypedRuleReference)
	}
}

func modifierValue(modifier materialization.Modifier, current formula.Type, registry *formula.Registry) (formula.Result, error) {
	decimal, err := formula.ParseDecimal(modifier.Value)
	if err != nil {
		return formula.Result{}, EvaluatorDiagnostic("NUMERIC_NON_FINITE")
	}
	if modifier.Operation == "Multiply" {
		scalar, found := registry.Unit("scalar")
		if !found {
			return formula.Result{}, ErrTypedRuleAdapterInvalid
		}
		return formula.Result{Type: formula.Type{ValueType: formula.DecimalType, Unit: scalar}, Decimal: &decimal}, nil
	}
	return formula.Result{Type: current, Decimal: &decimal}, nil
}

func applyModifier(operation string, current, value formula.Result, registry *formula.Registry) (formula.Result, error) {
	if current.Decimal == nil || value.Decimal == nil {
		return formula.Result{}, EvaluatorDiagnostic("FORMULA_TYPE_MISMATCH")
	}
	if operation == "Override" {
		if current.Type != value.Type {
			return formula.Result{}, EvaluatorDiagnostic("FORMULA_UNIT_MISMATCH")
		}
		return value, nil
	}
	var next formula.Decimal
	var err error
	switch operation {
	case "Add":
		if !registry.AllowsBinary(formula.OperationAdd, current.Type.Unit, value.Type.Unit) {
			return formula.Result{}, EvaluatorDiagnostic("FORMULA_UNIT_MISMATCH")
		}
		next, err = formula.Add(*current.Decimal, *value.Decimal)
	case "Multiply":
		if !registry.AllowsBinary(formula.OperationMultiply, current.Type.Unit, value.Type.Unit) {
			return formula.Result{}, EvaluatorDiagnostic("FORMULA_UNIT_MISMATCH")
		}
		next, err = formula.Multiply(*current.Decimal, *value.Decimal)
	case "Max":
		if !registry.AllowsBinary(formula.OperationCompare, current.Type.Unit, value.Type.Unit) {
			return formula.Result{}, EvaluatorDiagnostic("FORMULA_UNIT_MISMATCH")
		}
		next = *current.Decimal
		if value.Decimal.Compare(next) > 0 {
			next = *value.Decimal
		}
	case "Min":
		if !registry.AllowsBinary(formula.OperationCompare, current.Type.Unit, value.Type.Unit) {
			return formula.Result{}, EvaluatorDiagnostic("FORMULA_UNIT_MISMATCH")
		}
		next = *current.Decimal
		if value.Decimal.Compare(next) < 0 {
			next = *value.Decimal
		}
	default:
		return formula.Result{}, ErrTypedRuleAdapterInvalid
	}
	if err != nil {
		return formula.Result{}, EvaluatorDiagnostic("NUMERIC_OUT_OF_RANGE")
	}
	return formula.Result{Type: current.Type, Decimal: &next}, nil
}
