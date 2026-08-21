package scenario

import "testing"

func TestBuiltinRegistryProvidesFourStableImmutableTemplates(t *testing.T) {
	left, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	right, err := NewBuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(left.Templates()); got != 4 {
		t.Fatalf("templates=%d", got)
	}
	first, found := left.Get("single-target-30s", "v1")
	if !found || first.BodyHash == "" {
		t.Fatalf("template=%#v found=%v", first, found)
	}
	second, _ := right.Get("single-target-30s", "v1")
	if first.BodyHash != second.BodyHash || string(first.Body) != string(second.Body) {
		t.Fatalf("template is not stable: %q != %q", first.BodyHash, second.BodyHash)
	}
	first.Body[0] = '!'
	again, _ := left.Get("single-target-30s", "v1")
	if again.Body[0] == '!' {
		t.Fatal("registry returned mutable template body")
	}
}

func TestRegistryRejectsMismatchedAndUnknownReferences(t *testing.T) {
	template := BuiltinTemplates()[0]
	template.BodyHash = "bad"
	if _, err := NewRegistry([]Template{template}, []string{"damage-v1"}, []string{"combat-v1"}); err == nil {
		t.Fatal("expected body hash mismatch")
	}
	template = BuiltinTemplates()[0]
	if _, err := NewRegistry([]Template{template}, []string{"other"}, []string{"combat-v1"}); err == nil {
		t.Fatal("expected unknown event rejection")
	}
}
