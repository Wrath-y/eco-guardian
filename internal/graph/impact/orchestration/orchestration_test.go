package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	"github.com/zouyi/eco-guardian/internal/graph/impact/planner"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type queryFake struct {
	version, hash string
	calls         int
	err           error
}

func (f *queryFake) Traverse(context.Context, impact.TraverseRequest, string) (impact.TraverseResponse, error) {
	f.calls++
	return impact.TraverseResponse{ResolvedSnapshotVersion: f.version, ContentHash: f.hash}, f.err
}
func (f *queryFake) Paths(context.Context, impact.PathsRequest, string) (impact.PathsResponse, error) {
	f.calls++
	return impact.PathsResponse{ResolvedSnapshotVersion: f.version, ContentHash: f.hash}, f.err
}
func (f *queryFake) Retrieve(context.Context, impact.RetrieveRequest, string) (impact.RetrieveResponse, error) {
	f.calls++
	return impact.RetrieveResponse{ResolvedSnapshotVersion: f.version, ContentHash: f.hash, Mode: "hybrid"}, f.err
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

type testIDs struct{}

func (testIDs) New() (domain.ID, error) { return domain.NewID() }

func orchestrationFixture(t *testing.T) (*sqlite.Store, impact.Input, string, projector.Descriptor) {
	t.Helper()
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	store, _, err := sqlite.Create(context.Background(), t.TempDir(), registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	draft := domain.EntityDraft{Key: "impact_tag", Name: "Impact Tag", Payload: map[string]json.RawMessage{"category": json.RawMessage(`"element"`), "parent_tag_ids": json.RawMessage(`[]`)}}
	entity, base, err := store.Create(context.Background(), domain.KindTag, draft)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := json.Marshal("Impact Tag Changed")
	_, target, err := store.Patch(context.Background(), domain.KindTag, entity.ID, entity.EntityVersion, domain.EntityPatch{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	hashA, hashB, hashC := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	input := impact.Input{ProjectID: store.ProjectID(), Base: impact.RevisionIdentity{RevisionID: base.ID, ConfigHash: base.ConfigHash, VersionManifestHash: hashA, GraphManifestHash: hashB}, Target: impact.RevisionIdentity{RevisionID: target.ID, ConfigHash: target.ConfigHash, VersionManifestHash: hashA, GraphManifestHash: hashC}, Filters: impact.Filters{RelationshipKinds: []string{"explicit"}, Direction: impact.DirectionIncoming}}
	input, _, inputHash, err := planner.Normalize(input)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := projector.Descriptor{SchemaVersion: projector.ProjectionSchemaV1, Version: projector.ProjectorV1, Relations: projector.V1Relations(), Formatter: projector.V1Formatter{}}
	return store, input, inputHash, descriptor
}

func TestWorkerSealsReportJobAndExactHandoffAtomically(t *testing.T) {
	store, input, inputHash, descriptor := orchestrationFixture(t)
	clock := testClock{now: time.Now().UTC()}
	submitter := Submitter{Jobs: store, Events: store, Reports: store, Clock: clock}
	job, replay, err := submitter.Submit(context.Background(), input, inputHash, "manual-key", "manual")
	if err != nil || replay {
		t.Fatalf("job=%#v replay=%v err=%v", job, replay, err)
	}
	if _, err = store.CreateGraphImpactHandoff(context.Background(), input.Target.RevisionID, input.Target.GraphManifestHash, "impact"); err != nil {
		t.Fatal(err)
	}
	provider := &queryFake{version: string(input.Target.RevisionID), hash: input.Target.GraphManifestHash}
	worker := Worker{Revisions: store, Diffs: store, Provider: provider, Reports: store, Jobs: store, Events: store, IDs: testIDs{}, Clock: clock, Projector: descriptor}
	report, err := worker.Run(context.Background(), job.ID, "request")
	if err != nil || !report.ID.Valid() || provider.calls != 1 {
		t.Fatalf("report=%#v calls=%d err=%v", report, provider.calls, err)
	}
	completed, err := store.GetJob(context.Background(), job.ID)
	if err != nil || completed.Status != sharedjob.Succeeded || completed.Result == nil || completed.Result.ID != report.ID {
		t.Fatalf("job=%#v err=%v", completed, err)
	}
	if status, found, statusErr := store.GraphImpactHandoffStatus(context.Background(), input.Target.RevisionID, input.Target.GraphManifestHash); statusErr != nil || !found || status != "consumed" {
		t.Fatalf("handoff=%s found=%v err=%v", status, found, statusErr)
	}
	events, err := store.ListEvents(context.Background(), job.ID, 0)
	if err != nil || len(events) != 7 || events[len(events)-1].Progress != 100 || events[len(events)-1].Phase != string(PhaseFinalCommit) {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	if _, _, _, _, found, err := store.LoadStaging(context.Background(), job.ID); err != nil || found {
		t.Fatalf("staging found=%v err=%v", found, err)
	}
}

func TestSubmitDedupConflictAndExactCacheReuse(t *testing.T) {
	store, input, inputHash, descriptor := orchestrationFixture(t)
	clock := testClock{now: time.Now().UTC()}
	submitter := Submitter{Jobs: store, Reports: store, Clock: clock}
	first, _, err := submitter.Submit(context.Background(), input, inputHash, "same-key", "manual")
	if err != nil {
		t.Fatal(err)
	}
	provider := &queryFake{version: string(input.Target.RevisionID), hash: input.Target.GraphManifestHash}
	worker := Worker{Revisions: store, Diffs: store, Provider: provider, Reports: store, Jobs: store, Events: store, IDs: testIDs{}, Clock: clock, Projector: descriptor}
	report, err := worker.Run(context.Background(), first.ID, "request")
	if err != nil {
		t.Fatal(err)
	}
	replayed, replay, err := submitter.Submit(context.Background(), input, inputHash, "same-key", "manual")
	if err != nil || !replay || replayed.ID != first.ID {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, err)
	}
	changed := input
	changed.Filters.Direction = impact.DirectionOutgoing
	if _, _, err = submitter.Submit(context.Background(), changed, strings.Repeat("f", 64), "same-key", "manual"); err == nil {
		t.Fatal("idempotency conflict accepted")
	}
	cachedJob, replay, err := submitter.Submit(context.Background(), input, inputHash, "cache-key", "manual")
	if err != nil || replay || cachedJob.Status != sharedjob.Succeeded || cachedJob.Result == nil || cachedJob.Result.ID != report.ID || provider.calls != 1 {
		t.Fatalf("cached=%#v replay=%v calls=%d err=%v", cachedJob, replay, provider.calls, err)
	}
}

func TestCancellationRetainsDeterministicStagingAndNeverClaimsReady(t *testing.T) {
	store, input, inputHash, descriptor := orchestrationFixture(t)
	clock := testClock{now: time.Now().UTC()}
	submitter := Submitter{Jobs: store, Reports: store, Clock: clock}
	job, _, err := submitter.Submit(context.Background(), input, inputHash, "cancel-key", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.RequestCancellation(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	worker := Worker{Revisions: store, Diffs: store, Provider: &queryFake{version: string(input.Target.RevisionID), hash: input.Target.GraphManifestHash}, Reports: store, Jobs: store, Events: store, IDs: testIDs{}, Clock: clock, Projector: descriptor}
	if _, err = worker.Run(context.Background(), job.ID, "request"); err != nil {
		t.Fatal(err)
	}
	canceled, _ := store.GetJob(context.Background(), job.ID)
	if canceled.Status != sharedjob.Canceled || canceled.Result != nil {
		t.Fatalf("job=%#v", canceled)
	}
	if _, _, _, _, found, loadErr := store.LoadStaging(context.Background(), job.ID); loadErr != nil || !found {
		t.Fatalf("staging found=%v err=%v", found, loadErr)
	}
}

func TestDeterministicProviderFailureDoesNotCreateEmptyReport(t *testing.T) {
	store, input, inputHash, descriptor := orchestrationFixture(t)
	clock := testClock{now: time.Now().UTC()}
	submitter := Submitter{Jobs: store, Reports: store, Clock: clock}
	job, _, _ := submitter.Submit(context.Background(), input, inputHash, "failure-key", "manual")
	providerErr := errors.New("GRAPH_STORE_UNAVAILABLE")
	worker := Worker{Revisions: store, Diffs: store, Provider: &queryFake{version: string(input.Target.RevisionID), hash: input.Target.GraphManifestHash, err: providerErr}, Reports: store, Jobs: store, Events: store, IDs: testIDs{}, Clock: clock, Projector: descriptor}
	if _, err := worker.Run(context.Background(), job.ID, "request"); !errors.Is(err, providerErr) {
		t.Fatalf("error=%v", err)
	}
	failed, _ := store.GetJob(context.Background(), job.ID)
	if failed.Status != sharedjob.Failed || failed.Result != nil {
		t.Fatalf("job=%#v", failed)
	}
	if _, found, err := store.FindByInputHash(context.Background(), inputHash); err != nil || found {
		t.Fatalf("report found=%v err=%v", found, err)
	}
}

func TestRecoveryReplaysOnlyPersistedIdenticalImpactInput(t *testing.T) {
	store, input, inputHash, descriptor := orchestrationFixture(t)
	clock := testClock{now: time.Now().UTC()}
	submitter := Submitter{Jobs: store, Reports: store, Clock: clock}
	job, _, err := submitter.Submit(context.Background(), input, inputHash, "recover-key", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Transition(context.Background(), job.ID, sharedjob.Queued, sharedjob.Interrupted, nil, 0); err != nil {
		t.Fatal(err)
	}
	provider := &queryFake{version: string(input.Target.RevisionID), hash: input.Target.GraphManifestHash}
	worker := Worker{Revisions: store, Diffs: store, Provider: provider, Reports: store, Jobs: store, Events: store, IDs: testIDs{}, Clock: clock, Projector: descriptor}
	results, err := (Recovery{Jobs: store, Worker: worker}).Recover(context.Background(), 100)
	if err != nil || len(results) != 1 || results[0].Error != nil || !results[0].ReportID.Valid() || provider.calls != 1 {
		t.Fatalf("results=%#v calls=%d err=%v", results, provider.calls, err)
	}
	completed, _ := store.GetJob(context.Background(), job.ID)
	if completed.Status != sharedjob.Succeeded || completed.Result == nil || completed.Result.ID != results[0].ReportID {
		t.Fatalf("job=%#v", completed)
	}
}

var _ graphsync.ImpactScheduler = Scheduler{}
