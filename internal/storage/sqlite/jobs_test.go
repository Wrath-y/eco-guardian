package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

func sharedRequest(t *testing.T, store *Store, key string) sharedjob.Request {
	t.Helper()
	_, revision, err := store.Create(context.Background(), domain.KindTag, tagDraft("shared"+key))
	if err != nil {
		t.Fatal(err)
	}
	return sharedjob.Request{ProjectID: store.ProjectID(), Kind: "test", RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: key, RequestHash: strings.Repeat("b", 64)}
}

func TestSharedJobsPersistIdempotencyTransitionsAndCancellation(t *testing.T) {
	store := newStore(t)
	request := sharedRequest(t, store, "sharedjob")
	first, replay, err := store.CreateOrGet(context.Background(), request)
	if err != nil || replay || first.Status != sharedjob.Queued {
		t.Fatalf("first=%#v replay=%v err=%v", first, replay, err)
	}
	second, replay, err := store.CreateOrGet(context.Background(), request)
	if err != nil || !replay || second.ID != first.ID {
		t.Fatalf("second=%#v replay=%v err=%v", second, replay, err)
	}
	changed := request
	changed.RequestHash = strings.Repeat("c", 64)
	if _, _, err = store.CreateOrGet(context.Background(), changed); !errors.Is(err, ErrJobIdempotencyConflict) {
		t.Fatalf("idempotency error=%v", err)
	}
	running, swapped, err := store.Transition(context.Background(), first.ID, sharedjob.Queued, sharedjob.Running, nil, 0)
	if err != nil || !swapped || running.Status != sharedjob.Running {
		t.Fatalf("running=%#v swapped=%v err=%v", running, swapped, err)
	}
	canceled, replay, err := store.RequestCancellation(context.Background(), first.ID)
	if err != nil || replay || canceled.CancelGeneration != 1 || canceled.CancelRequestedAt == nil {
		t.Fatalf("canceled=%#v replay=%v err=%v", canceled, replay, err)
	}
	if _, swapped, err = store.Transition(context.Background(), first.ID, sharedjob.Running, sharedjob.Succeeded, &sharedjob.Result{Type: "test", ID: request.RevisionID, URL: "/runs/1"}, 0); err != nil || swapped {
		t.Fatalf("stale success swapped=%v err=%v", swapped, err)
	}
	final, swapped, err := store.Transition(context.Background(), first.ID, sharedjob.Running, sharedjob.Canceled, nil, 1)
	if err != nil || !swapped || final.Status != sharedjob.Canceled {
		t.Fatalf("final=%#v swapped=%v err=%v", final, swapped, err)
	}
	replayed, replay, err := store.RequestCancellation(context.Background(), first.ID)
	if err != nil || !replay || replayed.ID != first.ID {
		t.Fatalf("replay=%#v reused=%v err=%v", replayed, replay, err)
	}
}

func TestSharedJobEventsAreOrdinalAndResumable(t *testing.T) {
	store := newStore(t)
	job, _, err := store.CreateOrGet(context.Background(), sharedRequest(t, store, "sharedevents"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first := sharedjob.Event{JobID: job.ID, Ordinal: 1, Phase: "QUEUED", Progress: 0, CreatedAt: now}
	if _, replay, err := store.Append(context.Background(), first); err != nil || replay {
		t.Fatalf("first replay=%v err=%v", replay, err)
	}
	if _, replay, err := store.Append(context.Background(), first); err != nil || !replay {
		t.Fatalf("replay=%v err=%v", replay, err)
	}
	second := sharedjob.Event{JobID: job.ID, Ordinal: 2, Phase: "RUNNING", Progress: 25, CreatedAt: now.Add(time.Millisecond)}
	if _, _, err = store.Append(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Append(context.Background(), sharedjob.Event{JobID: job.ID, Ordinal: 3, Phase: "BAD", Progress: 24, CreatedAt: now.Add(2 * time.Millisecond)}); !errors.Is(err, ErrJobEvent) {
		t.Fatalf("decreasing progress error=%v", err)
	}
	events, err := store.ListEvents(context.Background(), job.ID, 1)
	if err != nil || len(events) != 1 || events[0].Ordinal != 2 {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}
