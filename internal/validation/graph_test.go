package validation

import "testing"

func TestStaticFormulaCyclesAreStableAndIncludeSelfEdges(t *testing.T) {
	a, b := referenceID(t), referenceID(t)
	first := FormulaNode{a, "/payload/attribute_values/0/expression", a}
	second := FormulaNode{b, "/payload/attribute_values/0/expression", b}
	cycles := StaticFormulaCycles([]FormulaNode{second, first}, []FormulaEdge{{second, first}, {first, second}})
	if len(cycles) != 2 || cycles[0].CycleHash != cycles[1].CycleHash {
		t.Fatalf("%#v", cycles)
	}
	self := StaticFormulaCycles([]FormulaNode{first}, []FormulaEdge{{first, first}})
	if len(self) != 1 || self[0].CycleHash == "" {
		t.Fatalf("%#v", self)
	}
}
