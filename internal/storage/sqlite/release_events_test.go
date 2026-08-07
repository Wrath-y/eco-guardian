package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

func TestReleaseJobEventsAreMonotonicPersistentAndDeduplicated(t *testing.T) {
	s := newStore(t)
	_, revision, err := s.Create(context.Background(), "tag", tagDraft("events"))
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := s.CreateOrGetReleaseJob(context.Background(), versioningrelease.JobRequest{ProjectID: s.ProjectID(), RevisionID: revision.ID, InputHash: revision.ConfigHash, IdempotencyKey: "events-1", RequestHash: strings.Repeat("e", 64)})
	if err != nil {
		t.Fatal(err)
	}
	first := versioningrelease.Event{JobID: job.ID, Ordinal: 1, Phase: "QUEUED", Progress: 0, CreatedAt: time.Now().UTC()}
	stored, deduplicated, err := s.AppendReleaseJobEvent(context.Background(), first)
	if err != nil || deduplicated || stored != first {
		t.Fatalf("first=%#v deduplicated=%v err=%v", stored, deduplicated, err)
	}
	if _, deduplicated, err = s.AppendReleaseJobEvent(context.Background(), first); err != nil || !deduplicated {
		t.Fatalf("replay deduplicated=%v err=%v", deduplicated, err)
	}
	changed := first
	changed.Progress = 1
	if _, _, err = s.AppendReleaseJobEvent(context.Background(), changed); !errors.Is(err, ErrReleaseJobEventConflict) {
		t.Fatalf("changed event error=%v", err)
	}
	second := versioningrelease.Event{JobID: job.ID, Ordinal: 2, Phase: "SUCCEEDED", Progress: 100, Result: &versioningrelease.JobResult{Type: "release", ID: mustID(t), URL: "/api/v1/releases/result"}, CreatedAt: first.CreatedAt.Add(time.Second)}
	if _, _, err = s.AppendReleaseJobEvent(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListReleaseJobEvents(context.Background(), job.ID, 0)
	if err != nil || len(events) != 2 || events[0].Ordinal != 1 || events[1].Ordinal != 2 || events[1].Result == nil {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	afterFirst, err := s.ListReleaseJobEvents(context.Background(), job.ID, 1)
	if err != nil || len(afterFirst) != 1 || afterFirst[0].Ordinal != 2 {
		t.Fatalf("after first=%#v err=%v", afterFirst, err)
	}
}
