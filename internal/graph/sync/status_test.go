package sync

import (
	"context"
	"reflect"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestPipelineMapperUpdatesOnlyMutableSyncState(t *testing.T) {
	revisionID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	states := &pipelineStateFake{state: SyncState{RevisionID: string(revisionID), Pipeline: StateQueued, Warnings: []string{}}, found: true}
	mapper := PipelineMapper{States: states}
	building, replay, err := mapper.MarkBuilding(context.Background(), revisionID)
	if err != nil || replay || building.Pipeline != StateBuilding || building.Generation != 1 {
		t.Fatalf("building=%#v replay=%v err=%v", building, replay, err)
	}
	if replayed, replay, err := mapper.MarkBuilding(context.Background(), revisionID); err != nil || !replay || !reflect.DeepEqual(replayed, building) {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, err)
	}
	failed, replay, err := mapper.MarkFailed(context.Background(), revisionID, "PROVIDER_UNAVAILABLE")
	if err != nil || replay || failed.Pipeline != StateFailed || failed.Generation != 2 || failed.SafeError != "PROVIDER_UNAVAILABLE" {
		t.Fatalf("failed=%#v replay=%v err=%v", failed, replay, err)
	}
	if replayed, replay, err := mapper.MarkFailed(context.Background(), revisionID, "PROVIDER_UNAVAILABLE"); err != nil || !replay || !reflect.DeepEqual(replayed, failed) {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, err)
	}
}

func TestComputeFreshnessNeverMutatesRevisionHistory(t *testing.T) {
	projectID, _ := domain.NewID()
	revisionID, _ := domain.NewID()
	jobID, _ := domain.NewID()
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	job := &GraphJob{ID: jobID, ProjectID: projectID, RevisionID: revisionID, InputHash: hash, IdempotencyKey: "freshness", RequestHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: JobSucceeded}
	state := SyncState{RevisionID: string(revisionID), Pipeline: StateReady, LatestJobID: string(jobID), Warnings: []string{}}
	if got := ComputeFreshness(FreshnessInput{RevisionID: revisionID, InputHash: hash, State: state, Job: job}); !got.Fresh || len(got.Reasons) != 0 {
		t.Fatalf("freshness=%#v", got)
	}
	before := state
	stale := ComputeFreshness(FreshnessInput{RevisionID: revisionID, InputHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", State: state, Job: job})
	if stale.Fresh || !reflect.DeepEqual(stale.Reasons, []string{"JOB_IDENTITY_MISMATCH"}) || !reflect.DeepEqual(state, before) {
		t.Fatalf("stale=%#v state=%#v", stale, state)
	}
}
