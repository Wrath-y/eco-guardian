package app

import (
	"context"
	"reflect"
	"testing"
)

type fakeService struct {
	events *[]string
	name   string
}

func (s fakeService) Start() error { *s.events = append(*s.events, "start-"+s.name); return nil }
func (s fakeService) Close(context.Context) error {
	*s.events = append(*s.events, "close-"+s.name)
	return nil
}

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

func TestCompositionRootStopsHTTPBeforeProjectManager(t *testing.T) {
	events := []string{}
	a, err := New(Config{Runtime: fakeService{&events, "http"}, Projects: fakeService{&events, "projects"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"start-http", "close-http", "close-projects"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("order = %v, want %v", events, want)
	}
}
