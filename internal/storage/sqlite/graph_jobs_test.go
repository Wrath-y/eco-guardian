package sqlite

import (
	"context"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"strings"
	"testing"
)

func TestGraphJobAdmissionIsIdempotent(t *testing.T) {
	store := newStore(t)
	_, revision, err := store.Create(context.Background(), "tag", tagDraft("graphjob"))
	if err != nil {
		t.Fatal(err)
	}
	request := graphsync.GraphJobRequest{ProjectID: store.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "graph-job-key", RequestHash: strings.Repeat("b", 64)}
	first, replay, err := store.CreateOrGetGraphJob(context.Background(), request)
	if err != nil || replay {
		t.Fatalf("%#v %v %v", first, replay, err)
	}
	second, replay, err := store.CreateOrGetGraphJob(context.Background(), request)
	if err != nil || !replay || second.ID != first.ID {
		t.Fatalf("%#v %v %v", second, replay, err)
	}
	request.RequestHash = strings.Repeat("c", 64)
	if _, _, err = store.CreateOrGetGraphJob(context.Background(), request); err != ErrGraphJobConflict {
		t.Fatalf("%v", err)
	}
}
