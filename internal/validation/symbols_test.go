package validation

import (
	"encoding/json"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

func TestAttributeSymbolsResolveReadableKeysAndLegacyIDs(t *testing.T) {
	registry, err := formula.V1Registry()
	if err != nil {
		t.Fatal(err)
	}
	attribute := domain.Entity{
		ID: "018f9e40-0000-7000-8000-000000000201", Kind: domain.KindAttribute, Key: "health", Status: domain.StatusActive,
		Payload: map[string]json.RawMessage{
			"value_type": json.RawMessage(`"decimal"`), "dimension": json.RawMessage(`"health"`), "base_unit": json.RawMessage(`"health_point"`),
		},
	}
	scalar, _ := registry.Unit("scalar")
	resolver := AttributeSymbols([]domain.Entity{attribute}, registry, formula.SymbolTable{"scenario:level": {ValueType: formula.DecimalType, Unit: scalar}})
	for _, symbol := range []string{"self:health", "source:health", "target:health", "self:" + string(attribute.ID)} {
		parts := splitFormulaSymbol(symbol)
		value, found := resolver.Resolve(parts[0], parts[1])
		if !found || value.Unit.Dimension != "health" {
			t.Fatalf("symbol %s resolved=%v found=%v", symbol, value, found)
		}
	}
	if _, found := resolver.Resolve("scenario", "level"); !found {
		t.Fatal("fallback scenario symbol was lost")
	}
}

func splitFormulaSymbol(value string) [2]string {
	for index := range value {
		if value[index] == ':' {
			return [2]string{value[:index], value[index+1:]}
		}
	}
	return [2]string{}
}
