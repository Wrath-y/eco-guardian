package formula

import "testing"

func TestASTGoldenV1(t *testing.T) {
	r, _ := V1Registry()
	result := Parse("1+2*3", r)
	encoded, err := result.AST.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":"ast-v1","root":{"kind":"binary","span":{"start_byte":0,"end_byte":5},"operator":"+","args":[{"kind":"decimal","span":{"start_byte":0,"end_byte":1},"text":"1"},{"kind":"binary","span":{"start_byte":2,"end_byte":5},"operator":"*","args":[{"kind":"decimal","span":{"start_byte":2,"end_byte":3},"text":"2"},{"kind":"decimal","span":{"start_byte":4,"end_byte":5},"text":"3"}]}]}}`
	if string(encoded) != want {
		t.Fatalf("AST golden\n got %s\nwant %s", encoded, want)
	}
	for _, source := range []string{"if(false, 1[ratio], 0[ratio])", "min(1[count], 2[count])", "max(1[count], 2[count])", "clamp(1[count], 0[count], 2[count])", "abs(1)", "floor(1)", "ceil(1)", "round(1)", "${target:甲}"} {
		if parsed := Parse(source, r); len(parsed.Errors) != 0 {
			t.Fatalf("%s: %#v", source, parsed.Errors)
		}
	}
	for _, source := range []string{"x = 1", "a.b", "[1]", "f(1)"} {
		if parsed := Parse(source, r); len(parsed.Errors) == 0 {
			t.Fatalf("forbidden %q accepted", source)
		}
	}
}
