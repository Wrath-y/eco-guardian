package formula

import "testing"

func TestParsePrecedenceSelectorsUnitsAndUnicodeSpans(t *testing.T) {
	r, _ := V1Registry()
	source := "min(1[damage_point], ${self:甲}) + 2[damage_point] * 3[scalar]"
	result := Parse(source, r)
	if len(result.Errors) != 0 {
		t.Fatalf("parse errors: %#v", result.Errors)
	}
	if result.AST.Root.Operator != "+" || result.AST.Root.Args[1].Operator != "*" {
		t.Fatalf("bad precedence: %#v", result.AST.Root)
	}
	if result.AST.Root.Span.EndByte != len(source) {
		t.Fatalf("byte span=%v length=%d", result.AST.Root.Span, len(source))
	}
}
func TestParseRejectsForbiddenSyntaxWithoutExecution(t *testing.T) {
	r, _ := V1Registry()
	for _, source := range []string{"unknown(1)", "a = 1", "${bad:x}", "foo.bar"} {
		if got := Parse(source, r); len(got.Errors) == 0 {
			t.Fatalf("%q unexpectedly parsed", source)
		}
	}
}

func TestParseCanonicalConstantsAndUnitRegistry(t *testing.T) {
	r, _ := V1Registry()
	result := Parse("001.200[ratio]", r)
	if len(result.Errors) != 0 || result.AST.Root.Text != "1.2" {
		t.Fatalf("unexpected constant: %#v", result)
	}
	if result := Parse("1[unknown]", r); len(result.Errors) == 0 {
		t.Fatal("unknown unit accepted")
	}
}
