package sqlite

import (
	"context"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

func TestGraphJobAdmissionIsIdempotent(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("graphjob"))
	if err != nil {
		t.Fatal(err)
	}
	request := graphsync.GraphJobRequest{ProjectID: store.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "graph-job-key", RequestHash: strings.Repeat("b", 64), Evidence: `{"validation_scope":"FULL"}`}
	first, replay, err := store.CreateOrGetGraphJob(context.Background(), request)
	if err != nil || replay {
		t.Fatalf("%#v %v %v", first, replay, err)
	}
	second, replay, err := store.CreateOrGetGraphJob(context.Background(), request)
	if err != nil || !replay || second.ID != first.ID {
		t.Fatalf("%#v %v %v", second, replay, err)
	}
	if second.Evidence != request.Evidence {
		t.Fatalf("evidence=%q", second.Evidence)
	}
	request.RequestHash = strings.Repeat("c", 64)
	if _, _, err = store.CreateOrGetGraphJob(context.Background(), request); err != ErrGraphJobConflict {
		t.Fatalf("%v", err)
	}
}

func TestGraphRetryJobIsLinkedAndIdempotent(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("graphretry"))
	if err != nil {
		t.Fatal(err)
	}
	previous, _, err := store.CreateOrGetGraphJob(context.Background(), graphsync.GraphJobRequest{ProjectID: store.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "automatic-job", RequestHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	retryRequest, err := graphsync.RetryGraphJobRequest(previous, "retry-intent")
	if err != nil {
		t.Fatal(err)
	}
	retry, replay, err := store.CreateOrGetGraphJob(context.Background(), retryRequest)
	if err != nil || replay || retry.RetryOfJobID != previous.ID {
		t.Fatalf("retry=%#v replay=%v err=%v", retry, replay, err)
	}
	stored, err := store.GetGraphJob(context.Background(), retry.ID)
	if err != nil || stored.RetryOfJobID != previous.ID {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	if replayed, replay, err := store.CreateOrGetGraphJob(context.Background(), retryRequest); err != nil || !replay || replayed.ID != retry.ID {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, err)
	}
	other, _, err := store.CreateOrGetGraphJob(context.Background(), graphsync.GraphJobRequest{ProjectID: store.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "other-job", RequestHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	conflicting, err := graphsync.RetryGraphJobRequest(other, "retry-intent")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.CreateOrGetGraphJob(context.Background(), conflicting); err != ErrGraphJobConflict {
		t.Fatalf("conflicting retry err=%v", err)
	}
	missingID, _ := domain.NewID()
	invalid := retryRequest
	invalid.IdempotencyKey, invalid.RetryOfJobID, invalid.RequestHash = "missing-retry", missingID, strings.Repeat("c", 64)
	if _, _, err = store.CreateOrGetGraphJob(context.Background(), invalid); err != ErrGraphJobRetry {
		t.Fatalf("missing retry err=%v", err)
	}
}

func TestGraphJobTransitionsAndCheckpointEventsAreMonotonic(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("graphcheckpoints"))
	if err != nil {
		t.Fatal(err)
	}
	request := graphsync.GraphJobRequest{ProjectID: store.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "graph-checkpoint-key", RequestHash: strings.Repeat("b", 64)}
	job, _, err := store.CreateOrGetGraphJob(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != graphsync.JobQueued {
		t.Fatalf("job=%#v", job)
	}
	if _, swapped, err := store.TransitionGraphJob(context.Background(), job.ID, graphsync.JobQueued, graphsync.JobRunning, nil); err != nil || !swapped {
		t.Fatalf("running swapped=%v err=%v", swapped, err)
	}
	first := graphsync.GraphJobEvent{JobID: job.ID, Ordinal: 1, Phase: graphsync.PhaseQueued, Progress: 0}
	if _, replay, err := store.AppendGraphJobEvent(context.Background(), first); err != nil || replay {
		t.Fatalf("first replay=%v err=%v", replay, err)
	}
	if _, replay, err := store.AppendGraphJobEvent(context.Background(), first); err != nil || !replay {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	second := graphsync.GraphJobEvent{JobID: job.ID, Ordinal: 2, Phase: graphsync.PhaseValidationConfirmed, Progress: 10}
	if _, _, err := store.AppendGraphJobEvent(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AppendGraphJobEvent(context.Background(), graphsync.GraphJobEvent{JobID: job.ID, Ordinal: 3, Phase: graphsync.PhaseProjected, Progress: 9}); err != ErrGraphJobEvent {
		t.Fatalf("decreasing progress err=%v", err)
	}
	if _, _, err := store.AppendGraphJobEvent(context.Background(), graphsync.GraphJobEvent{JobID: job.ID, Ordinal: 3, Phase: graphsync.PhaseReady, Progress: 100}); err != ErrGraphJobEvent {
		t.Fatalf("skipped phase err=%v", err)
	}
	result := &graphsync.GraphJobResult{Type: "graph_sync", ID: revision.ID, URL: "/api/v1/revisions/" + string(revision.ID) + "/graph-status"}
	finished, swapped, err := store.TransitionGraphJob(context.Background(), job.ID, graphsync.JobRunning, graphsync.JobSucceeded, result)
	if err != nil || !swapped || finished.Status != graphsync.JobSucceeded || finished.Result == nil || *finished.Result != *result {
		t.Fatalf("finished=%#v swapped=%v err=%v", finished, swapped, err)
	}
	if replayed, replay, replayErr := store.TransitionGraphJob(context.Background(), job.ID, graphsync.JobSucceeded, graphsync.JobSucceeded, result); replayErr != nil || !replay || replayed.ID != job.ID {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, replayErr)
	}
	events, err := store.ListGraphJobEvents(context.Background(), job.ID, 0)
	if err != nil || len(events) != 2 || events[1] != second {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	if _, swapped, err = store.TransitionGraphJob(context.Background(), job.ID, graphsync.JobRunning, graphsync.JobFailed, nil); err != nil || swapped {
		t.Fatalf("stale swapped=%v err=%v", swapped, err)
	}
}
