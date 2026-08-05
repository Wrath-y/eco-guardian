package validation

import (
	"github.com/zouyi/eco-guardian/internal/formula"
	"testing"
)

func TestCompileFormulaBindingsUsesOnlyProvidedSymbols(t *testing.T) {
	registry, _ := formula.V1Registry()
	id := referenceID(t)
	output := referenceID(t)
	bindings := []FormulaTuple{{SourceID: id, FieldPath: "/payload/attribute_values/0/expression", OutputAttributeID: output, Expression: "${scenario:level} + 1"}}
	scalar, _ := registry.Unit("scalar")
	records, issues := CompileFormulaBindings(bindings, registry, formula.SymbolTable{"scenario:level": {ValueType: formula.DecimalType, Unit: scalar}})
	if len(issues) != 0 || len(records) != 1 || len(records[0].Reads) != 1 || records[0].ASTHash == "" {
		t.Fatalf("%#v %#v", records, issues)
	}
	_, issues = CompileFormulaBindings(bindings, registry, formula.SymbolTable{})
	if len(issues) != 1 || issues[0].Code != "FORMULA_UNKNOWN_VARIABLE" {
		t.Fatalf("%#v", issues)
	}
}
