package orchestration

import (
	"context"
	"errors"
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

type cancellationConvergingFake struct{ job sharedjob.Record }

func (fake *cancellationConvergingFake) CreateOrGet(context.Context, sharedjob.Request) (sharedjob.Record, bool, error) {
	return fake.job, false, nil
}
func (fake *cancellationConvergingFake) GetJob(context.Context, domain.ID) (sharedjob.Record, error) {
	return fake.job, nil
}
func (fake *cancellationConvergingFake) Transition(_ context.Context, _ domain.ID, expected, next sharedjob.Status, _ *sharedjob.Result, generation int64) (sharedjob.Record, bool, error) {
	if fake.job.Status != expected || generation != fake.job.CancelGeneration {
		return sharedjob.Record{}, false, errors.New("unexpected transition")
	}
	fake.job.Status = next
	return fake.job, true, nil
}
func (fake *cancellationConvergingFake) RequestCancellation(context.Context, domain.ID) (sharedjob.Record, bool, error) {
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

func TestConvergeCancellationTransitionsOnceAndIsIdempotent(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	fake := &cancellationConvergingFake{job: sharedjob.Record{ID: id, ProjectID: id, Kind: "simulation", InputHash: strings.Repeat("a", 64), IdempotencyKey: "cancel", RequestHash: strings.Repeat("b", 64), Status: sharedjob.Running, CancelGeneration: 1, CancelRequestedAt: &now, CreatedAt: now, UpdatedAt: now}}
	updated, changed, err := ConvergeCancellation(context.Background(), fake, id)
	if err != nil || !changed || updated.Status != sharedjob.Canceled {
		t.Fatalf("updated=%#v changed=%v err=%v", updated, changed, err)
	}
	updated, changed, err = ConvergeCancellation(context.Background(), fake, id)
	if err != nil || !changed || updated.Status != sharedjob.Canceled {
		t.Fatalf("repeat updated=%#v changed=%v err=%v", updated, changed, err)
	}
}
