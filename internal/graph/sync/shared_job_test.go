package sync

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func sharedAdapterID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSharedJobAdapterRoundTripsGraphJob(t *testing.T) {
	now := time.Now().UTC()
	original := GraphJob{ID: sharedAdapterID(t), RetryOfJobID: sharedAdapterID(t), ProjectID: sharedAdapterID(t), RevisionID: sharedAdapterID(t), InputHash: strings.Repeat("a", 64), IdempotencyKey: "graph-1", RequestHash: strings.Repeat("b", 64), Evidence: "stable", Status: JobSucceeded, Result: &GraphJobResult{Type: "graph_sync", ID: sharedAdapterID(t), URL: "/api/v1/graph"}, CreatedAt: now, UpdatedAt: now}
	record, err := original.SharedRecord()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := GraphJobFromSharedRecord(record, original.RetryOfJobID, original.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, original) {
		t.Fatalf("round trip mismatch: %#v", actual)
	}
}
