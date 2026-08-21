package orchestration

import (
	"context"
	"strings"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type jobStoreFake struct {
	request sharedjob.Request
	replay  bool
}

func (fake *jobStoreFake) CreateOrGet(_ context.Context, request sharedjob.Request) (sharedjob.Record, bool, error) {
	fake.request = request
	id, _ := domain.NewID()
	return sharedjob.Record{ID: id, ProjectID: request.ProjectID, Kind: request.Kind, RevisionID: request.RevisionID, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, Status: sharedjob.Queued}, fake.replay, nil
}
func (fake *jobStoreFake) GetJob(context.Context, domain.ID) (sharedjob.Record, error) {
	return sharedjob.Record{}, nil
}
func (fake *jobStoreFake) Transition(context.Context, domain.ID, sharedjob.Status, sharedjob.Status, *sharedjob.Result, int64) (sharedjob.Record, bool, error) {
	return sharedjob.Record{}, false, nil
}
func (fake *jobStoreFake) RequestCancellation(context.Context, domain.ID) (sharedjob.Record, bool, error) {
	return sharedjob.Record{}, false, nil
}
func jobID(t *testing.T) domain.ID {
	t.Helper()
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSimulationJobUsesSharedRequestAndAutomaticInputKey(t *testing.T) {
	input := strings.Repeat("a", 64)
	key, err := AutomaticIdempotencyKey(input)
	if err != nil || !strings.HasPrefix(key, "simulation:auto:") {
		t.Fatalf("key=%q err=%v", key, err)
	}
	store := &jobStoreFake{replay: true}
	request := sharedjob.Request{ProjectID: jobID(t), Kind: "simulation", RevisionID: jobID(t), InputHash: input, RequestHash: strings.Repeat("b", 64), IdempotencyKey: key}
	_, replay, err := CreateSimulationJob(context.Background(), store, request)
	if err != nil || !replay || store.request != request {
		t.Fatalf("request=%#v replay=%v err=%v", store.request, replay, err)
	}
	if _, err = AutomaticIdempotencyKey("short"); err == nil {
		t.Fatal("expected invalid hash rejection")
	}
}
