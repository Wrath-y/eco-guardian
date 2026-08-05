package formula

import "testing"

func FuzzParse(f *testing.F) {
	for _, seed := range []string{"", "1+2", "${self:018f1e68-7f8f-7b75-8000-000000000001}", "if(false,1/0,2)", "\xff\x00${"} {
		f.Add(seed)
	}
	registry, err := V1Registry()
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, source string) {
		first := Parse(source, registry)
		second := Parse(source, registry)
		firstBytes, err := first.AST.CanonicalBytes()
		if err != nil {
			t.Fatal(err)
		}
		secondBytes, err := second.AST.CanonicalBytes()
		if err != nil {
			t.Fatal(err)
		}
		if string(firstBytes) != string(secondBytes) || len(first.Errors) != len(second.Errors) {
			t.Fatalf("nondeterministic parse for %q", source)
		}
	})
}
