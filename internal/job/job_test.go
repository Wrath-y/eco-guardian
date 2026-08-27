package job

import (
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func testID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func validRequest(t *testing.T) Request {
	t.Helper()
	return Request{ProjectID: testID(t), Kind: "example", RevisionID: testID(t), InputHash: strings.Repeat("a", 64), IdempotencyKey: "retry-1", RequestHash: strings.Repeat("b", 64)}
}

func validRecord(t *testing.T) Record {
	t.Helper()
	now := time.Now().UTC()
	request := validRequest(t)
	return Record{ID: testID(t), ProjectID: request.ProjectID, Kind: request.Kind, RevisionID: request.RevisionID, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, Status: Queued, CreatedAt: now, UpdatedAt: now}
}

func TestRequestAndRecordValidateImmutableIdentity(t *testing.T) {
	record := validRecord(t)
	if !record.Valid() || !record.Request().Valid() {
		t.Fatal("expected valid record and request")
	}
	record.RequestHash = strings.Repeat("Z", 64)
	if record.Valid() {
		t.Fatal("invalid request hash must invalidate record")
	}
}

func TestRequestEquivalenceDistinguishesIdempotencyConflict(t *testing.T) {
	request := validRequest(t)
	if !request.Equivalent(request) {
		t.Fatal("a request must equal itself")
	}
	conflict := request
	conflict.RequestHash = strings.Repeat("c", 64)
	if request.Equivalent(conflict) {
		t.Fatal("a changed canonical request must conflict for the same key")
	}
}

func TestStatusTransitionsAndResultRequirements(t *testing.T) {
	statuses := []Status{Queued, Running, Succeeded, Failed, Canceled, Interrupted}
	allowed := map[Status]map[Status]bool{
		Queued:      {Running: true, Failed: true, Canceled: true, Interrupted: true},
		Running:     {Succeeded: true, Failed: true, Canceled: true, Interrupted: true},
		Interrupted: {Succeeded: true, Failed: true, Canceled: true},
	}
	for _, current := range statuses {
		if !current.Valid() {
			t.Fatalf("declared status is invalid: %q", current)
		}
		for _, next := range statuses {
			if got, want := current.CanTransitionTo(next), allowed[current][next]; got != want {
				t.Fatalf("transition %s -> %s = %v want %v", current, next, got, want)
			}
		}
	}
	record := validRecord(t)
	record.Status = Succeeded
	if record.Valid() {
		t.Fatal("successful job requires a result")
	}
	record.Result = &Result{Type: "example", ID: testID(t), URL: "/api/v1/examples/1"}
	if !record.Valid() {
		t.Fatal("expected successful record with a valid result")
	}
}

func TestCancellationGenerationIsExplicitAndBlocksSuccess(t *testing.T) {
	record := validRecord(t)
	if err := RequireUncanceled(record, 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record.CancelGeneration, record.CancelRequestedAt = 1, &now
	if !record.Valid() {
		t.Fatal("expected valid cancellation intent")
	}
	if RequireUncanceled(record, 0) == nil || RequireUncanceled(record, 1) == nil {
		t.Fatal("canceled job must not be sealable")
	}
}

func TestEventValidationRequiresOrdinalAndSafePayload(t *testing.T) {
	event := Event{JobID: testID(t), Ordinal: 1, Phase: "MATERIALIZED", Progress: 10, CreatedAt: time.Now().UTC()}
	if !event.Valid() {
		t.Fatal("expected valid event")
	}
	event.Ordinal = 0
	if event.Valid() {
		t.Fatal("zero ordinal must be rejected")
	}
}
