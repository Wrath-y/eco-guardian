package sync

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/validation"
)

type validationGateFake struct{ result validation.GateResult }

func (f validationGateFake) Check(context.Context, domain.ID, string, validation.VersionManifest) (validation.GateResult, error) {
	return f.result, nil
}

type jobAdmissionFake struct {
	calls   int
	replay  bool
	job     GraphJob
	request GraphJobRequest
}

func (f *jobAdmissionFake) CreateOrGetGraphJob(_ context.Context, request GraphJobRequest) (GraphJob, bool, error) {
	f.calls++
	f.request = request
	return f.job, f.replay, nil
}

func TestRetryAdmissionCreatesDistinctLinkedIntent(t *testing.T) {
	projectID, _ := domain.NewID()
	revisionID, _ := domain.NewID()
	previousID, _ := domain.NewID()
	jobID, _ := domain.NewID()
	previous := GraphJob{ID: previousID, ProjectID: projectID, RevisionID: revisionID, InputHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", IdempotencyKey: "automatic", RequestHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: JobFailed}
	versions := validation.VersionManifest{Schema: "s", DSL: "d", Registry: "r", NumericPolicy: "n"}
	jobs := &jobAdmissionFake{job: GraphJob{ID: jobID, ProjectID: projectID, RevisionID: revisionID, InputHash: previous.InputHash, IdempotencyKey: "retry-1", RequestHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Status: JobQueued}}
	service := AdmissionService{Validation: validationGateFake{validation.GatePass}, Jobs: jobs}
	if _, replay, err := service.AdmitRetry(context.Background(), previous, "retry-1", versions); err != nil || replay || jobs.calls != 1 || jobs.request.RetryOfJobID != previous.ID {
		t.Fatalf("request=%#v replay=%v err=%v", jobs.request, replay, err)
	}
	var evidence struct {
		Intent       string `json:"intent"`
		RetryOfJobID string `json:"retry_of_job_id"`
	}
	if err := json.Unmarshal([]byte(jobs.request.Evidence), &evidence); err != nil || evidence.Intent != "retry" || evidence.RetryOfJobID != string(previous.ID) {
		t.Fatalf("evidence=%q err=%v", jobs.request.Evidence, err)
	}
	if _, _, err := service.AdmitRetry(context.Background(), previous, "retry-1", validation.VersionManifest{}); err == nil || jobs.calls != 1 {
		t.Fatal("retry admission bypassed exact validation")
	}
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
