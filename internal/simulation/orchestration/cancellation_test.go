package orchestration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type cancellationStoreFake struct{ job sharedjob.Record }

func (fake cancellationStoreFake) CreateOrGet(context.Context, sharedjob.Request) (sharedjob.Record, bool, error) {
	return fake.job, false, nil
}
func (fake cancellationStoreFake) GetJob(context.Context, domain.ID) (sharedjob.Record, error) {
	return fake.job, nil
}
func (fake cancellationStoreFake) Transition(context.Context, domain.ID, sharedjob.Status, sharedjob.Status, *sharedjob.Result, int64) (sharedjob.Record, bool, error) {
	return fake.job, false, nil
}
func (fake cancellationStoreFake) RequestCancellation(context.Context, domain.ID) (sharedjob.Record, bool, error) {
	return fake.job, false, nil
}

func TestRequireUncanceledRejectsPersistedGenerationChange(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	request := sharedjob.Request{ProjectID: id, Kind: "simulation", InputHash: strings.Repeat("a", 64), IdempotencyKey: "cancel", RequestHash: strings.Repeat("b", 64)}
	job := sharedjob.Record{ID: id, ProjectID: id, Kind: request.Kind, InputHash: request.InputHash, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash, Status: sharedjob.Running, CreatedAt: now, UpdatedAt: now}
	if err = RequireUncanceled(context.Background(), cancellationStoreFake{job: job}, id, 0); err != nil {
		t.Fatal(err)
	}
	job.CancelGeneration, job.CancelRequestedAt = 1, &now
	if err = RequireUncanceled(context.Background(), cancellationStoreFake{job: job}, id, 0); err == nil {
		t.Fatal("expected cancellation generation rejection")
	}
}
