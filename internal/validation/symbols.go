package validation

import (
	"encoding/json"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

type attributeSymbolResolver struct {
	attributes formula.SymbolTable
	fallback   formula.SymbolResolver
}

func (r attributeSymbolResolver) Resolve(scope, symbol string) (formula.Type, bool) {
	if value, found := r.attributes.Resolve(scope, symbol); found {
		return value, true
	}
	if r.fallback == nil {
		return formula.Type{}, false
	}
	return r.fallback.Resolve(scope, symbol)
}

// AttributeSymbols exposes active attribute keys as readable formula symbols.
// Stable IDs remain aliases so historical expressions continue to validate.
func AttributeSymbols(entities []domain.Entity, registry *formula.Registry, fallback formula.SymbolResolver) formula.SymbolResolver {
	symbols := formula.SymbolTable{}
	for _, attribute := range entities {
		typeInfo, ok := AttributeFormulaType(attribute, registry)
		if !ok {
			continue
		}
		for _, scope := range []string{"self", "source", "target"} {
			symbols[scope+":"+attribute.Key] = typeInfo
			symbols[scope+":"+string(attribute.ID)] = typeInfo
		}
	}
	return attributeSymbolResolver{attributes: symbols, fallback: fallback}
}

func AttributeFormulaType(attribute domain.Entity, registry *formula.Registry) (formula.Type, bool) {
	if registry == nil || attribute.Kind != domain.KindAttribute || attribute.Status != domain.StatusActive {
		return formula.Type{}, false
	}
	var valueType, baseUnit, dimension string
	if json.Unmarshal(attribute.Payload["value_type"], &valueType) != nil {
		return formula.Type{}, false
	}
	if valueType == string(formula.BooleanType) {
		return formula.Type{ValueType: formula.BooleanType}, true
	}
	if json.Unmarshal(attribute.Payload["base_unit"], &baseUnit) != nil || json.Unmarshal(attribute.Payload["dimension"], &dimension) != nil {
		return formula.Type{}, false
	}
	unit, found := registry.Unit(baseUnit)
	if !found || string(unit.ValueType) != valueType || unit.Dimension != dimension {
		return formula.Type{}, false
	}
	return formula.Type{ValueType: unit.ValueType, Unit: unit}, true
}

func OutputAttributeFormulaType(attributes []domain.Entity, id domain.ID, registry *formula.Registry) (formula.Type, bool) {
	for _, attribute := range attributes {
		if attribute.ID == id {
			return AttributeFormulaType(attribute, registry)
		}
	}
	return formula.Type{}, false
}
