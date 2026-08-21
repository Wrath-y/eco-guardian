package release

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func adapterID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSharedJobAdapterRoundTripsReleaseJob(t *testing.T) {
	now := time.Now().UTC()
	original := Job{ID: adapterID(t), ProjectID: adapterID(t), RevisionID: adapterID(t), InputHash: strings.Repeat("a", 64), IdempotencyKey: "release-1", RequestHash: strings.Repeat("b", 64), Status: JobSucceeded, Result: &JobResult{Type: "release", ID: adapterID(t), URL: "/api/v1/releases/1"}, CreatedAt: now, UpdatedAt: now}
	record, err := original.SharedRecord()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := JobFromSharedRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, original) {
		t.Fatalf("round trip mismatch: %#v", actual)
	}
}
