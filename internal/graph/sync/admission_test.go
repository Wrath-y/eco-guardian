package sync

import (
	"context"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

type validationGateFake struct{ result validation.GateResult }

func (f validationGateFake) Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error) {
	return f.result, nil
}

type jobAdmissionFake struct {
	calls  int
	replay bool
	job    GraphJob
}

func (f *jobAdmissionFake) CreateOrGetGraphJob(_ context.Context, request GraphJobRequest) (GraphJob, bool, error) {
	f.calls++
	return f.job, f.replay, nil
}

func TestAdmissionServiceInvokesJobsOnlyAfterPass(t *testing.T) {
	id, _ := domain.NewID()
	jobID, _ := domain.NewID()
	request := GraphJobRequest{ProjectID: id, RevisionID: id, InputHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "key", RequestHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	versions := validation.VersionManifest{Schema: "s", DSL: "d", Registry: "r", NumericPolicy: "n"}
	jobs := &jobAdmissionFake{replay: true, job: GraphJob{ID: jobID, ProjectID: id, RevisionID: id, InputHash: request.InputHash}}
	service := AdmissionService{Validation: validationGateFake{validation.GatePass}, Jobs: jobs}
	if _, replay, err := service.Admit(context.Background(), request, versions); err != nil || !replay || jobs.calls != 1 {
		t.Fatalf("%v %v", replay, err)
	}
	service.Validation = validationGateFake{validation.GateBlocked}
	if _, _, err := service.Admit(context.Background(), request, versions); err == nil || jobs.calls != 1 {
		t.Fatal("blocked admission called jobs")
	}
}

func TestRequireFullValidationAcceptsOnlyExactPass(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	versions := validation.VersionManifest{Schema: "s", DSL: "d", Registry: "r", NumericPolicy: "n"}
	if err = RequireFullValidation(context.Background(), validationGateFake{validation.GatePass}, id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", versions); err != nil {
		t.Fatal(err)
	}
	for _, result := range []validation.GateResult{validation.GateBlocked, validation.GateRequiresValidation} {
		if RequireFullValidation(context.Background(), validationGateFake{result}, id, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", versions) == nil {
			t.Fatal(result)
		}
	}
}
