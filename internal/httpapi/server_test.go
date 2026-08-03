package httpapi

import (
	"context"
	"testing"
)

func TestRuntimeRejectsNonLoopbackBind(t *testing.T) {
	if _, err := NewRuntime("0.0.0.0:8080"); err == nil {
		t.Fatal("accepted non-loopback bind")
	}
}
func TestRuntimeStartsAndStops(t *testing.T) {
	r, err := NewRuntime("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Start(); err != nil {
		t.Fatal(err)
	}
	if r.Address() == "" {
		t.Fatal("no listener")
	}
	if err = r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
