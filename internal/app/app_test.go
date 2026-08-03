package app

import (
	"context"
	"testing"
)

func TestCompositionRootStartsAndStopsWithoutProject(t *testing.T) {
	a, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	if !a.Started() {
		t.Fatal("application should be started")
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.Started() {
		t.Fatal("application should be stopped")
	}
}
