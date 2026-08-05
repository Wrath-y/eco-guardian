package formula

import "testing"

func TestInferenceAndShortCircuitEvaluation(t *testing.T) {
	r, _ := V1Registry()
	parsed := Parse("if(false, 1[damage_point] / 0[scalar], min(3[damage_point], 2[damage_point]))", r)
	if len(parsed.Errors) != 0 {
		t.Fatal(parsed.Errors)
	}
	result, issues := Evaluate(parsed.AST.Root, r)
	if len(issues) != 0 || result.Decimal == nil || result.Decimal.String() != "2" {
		t.Fatalf("%#v %#v", result, issues)
	}
	parsed = Parse("1[damage_point] + 1[ratio]", r)
	_, issues = Infer(parsed.AST.Root, r, SymbolTable{})
	if len(issues) == 0 || issues[0].Code != "FORMULA_UNIT_MISMATCH" {
		t.Fatalf("%#v", issues)
	}
	parsed = Parse("${self:unknown}", r)
	_, issues = Infer(parsed.AST.Root, r, SymbolTable{})
	if len(issues) == 0 || issues[0].Code != "FORMULA_UNKNOWN_VARIABLE" {
		t.Fatalf("%#v", issues)
	}
	damage, _ := r.Unit("damage_point")
	scalar, _ := r.Unit("scalar")
	for _, scope := range []string{"self", "source", "target", "scenario"} {
		parsed = Parse("${"+scope+":known}", r)
		issues = InferOutput(parsed.AST.Root, Type{DecimalType, damage}, r, SymbolTable{scope + ":known": {DecimalType, damage}})
		if len(issues) != 0 {
			t.Fatalf("%s: %#v", scope, issues)
		}
	}
	parsed = Parse("1[ratio]", r)
	issues = InferOutput(parsed.AST.Root, Type{DecimalType, scalar}, r, SymbolTable{})
	if len(issues) == 0 || issues[0].Code != "FORMULA_TYPE_MISMATCH" {
		t.Fatalf("output mismatch: %#v", issues)
	}
	parsed = Parse("clamp(abs(-2.6), 1, 2)", r)
	result, issues = Evaluate(parsed.AST.Root, r)
	if len(issues) != 0 || result.Decimal.String() != "2" {
		t.Fatalf("builtins: %#v %#v", result, issues)
	}
	parsed = Parse("ceil(1.2) == 2", r)
	result, issues = Evaluate(parsed.AST.Root, r)
	if len(issues) != 0 || result.Boolean == nil || !*result.Boolean {
		t.Fatalf("comparison: %#v %#v", result, issues)
	}
	parsed = Parse("1 / 0", r)
	_, issues = Evaluate(parsed.AST.Root, r)
	if len(issues) == 0 || issues[0].Code != "FORMULA_DIVISION_BY_ZERO" {
		t.Fatalf("division: %#v", issues)
	}
	parsed = Parse("1e6144 * 10", r)
	_, issues = Evaluate(parsed.AST.Root, r)
	if len(issues) == 0 || issues[0].Code != "NUMERIC_OUT_OF_RANGE" {
		t.Fatalf("overflow: %#v", issues)
	}
}
