package versioning

import (
	"bytes"
	"testing"
)

func TestCanonicalJSONAndHashIgnoreMapRegistrationOrder(t *testing.T) {
	first := map[string]any{"policy": map[string]any{"required": true, "samples": float64(1000)}, "capability": "risk"}
	second := map[string]any{"capability": "risk", "policy": map[string]any{"samples": float64(1000), "required": true}}
	left, err := CanonicalJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalJSON(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) || SHA256(left) != SHA256(right) {
		t.Fatalf("canonical identity changed: %s != %s", left, right)
	}
}
