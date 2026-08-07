package project

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

func TestSQLiteFactoryRunsConfiguredRecoveryOnProjectOpen(t *testing.T) {
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	created, _, err := store.Create(context.Background(), directory, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := created.Close(); err != nil {
		t.Fatal(err)
	}
	called := false
	factory := SQLiteFactory{Registry: registry, Recover: func(_ context.Context, opened *store.Store) error {
		called = opened.ProjectID().Valid()
		return nil
	}}
	handle, err := factory.Open(context.Background(), directory)
	if err != nil || !called {
		t.Fatalf("handle=%v called=%v err=%v", handle, called, err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
}
