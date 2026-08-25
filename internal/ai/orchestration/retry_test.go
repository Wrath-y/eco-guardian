package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type explicitRetryRepositoryFake struct {
	basis   ExplicitRetryBasis
	records map[string]ExplicitRetryRecord
	loads   int
	creates int
}

func (repository *explicitRetryRepositoryFake) LoadExplicitRetryBasis(_ context.Context, jobID domain.ID) (ExplicitRetryBasis, error) {
	repository.loads++
	if repository.basis.ParentJob.ID != jobID {
		return ExplicitRetryBasis{}, ErrExplicitRetryInvalid
	}
	return repository.basis, nil
}

func (repository *explicitRetryRepositoryFake) FindExplicitRetry(_ context.Context, parentJobID domain.ID, key string) (ExplicitRetryRecord, bool, error) {
	record, found := repository.records[string(parentJobID)+"\x00"+key]
	return record, found, nil
}

func (repository *explicitRetryRepositoryFake) CreateExplicitRetry(_ context.Context, command ExplicitRetryCommand) (ExplicitRetryRecord, bool, error) {
	if !command.Valid() {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryInvalid
	}
	key := string(command.Attempt.ParentJobID) + "\x00" + command.JobRequest.IdempotencyKey
	if existing, found := repository.records[key]; found {
		return existing, true, nil
	}
	now := time.Unix(1_700_000_200, 0).UTC()
	job := sharedjob.Record{
		ID: command.JobID, ProjectID: command.JobRequest.ProjectID, Kind: command.JobRequest.Kind, RevisionID: command.JobRequest.RevisionID,
		InputHash: command.JobRequest.InputHash, IdempotencyKey: command.JobRequest.IdempotencyKey, RequestHash: command.JobRequest.RequestHash,
		Status: sharedjob.Queued, CreatedAt: now, UpdatedAt: now,
	}
	record := ExplicitRetryRecord{State: AIJobState{Job: job, Phase: command.InitialPhase}, Attempt: cloneAttemptRecord(command.Attempt)}
	if !record.Valid() {
		return ExplicitRetryRecord{}, false, ErrExplicitRetryInvalid
	}
	repository.records[key] = record
	repository.creates++
	return record, false, nil
}

type explicitRetrySettingsFake struct {
	capability aiprovider.CapabilityState
	calls      []RetrySettingsRequest
}

func (source *explicitRetrySettingsFake) ResolveExplicitRetrySettings(_ context.Context, request RetrySettingsRequest) (RetrySettingsResolution, error) {
	source.calls = append(source.calls, request)
	if source.capability != aiprovider.CapabilityAvailable && source.capability != aiprovider.CapabilityDegraded {
		return RetrySettingsResolution{Capability: source.capability}, nil
	}
	manifest := attemptLedgerManifest(request.AttemptID)
	manifest.InputHash = request.InputHash
	manifest.EvidenceManifestHash = request.EvidenceManifestHash
	manifest.CancelGeneration = request.CancelGeneration
	return RetrySettingsResolution{Capability: source.capability, Manifest: manifest}, nil
}

type explicitRetryIDsFake struct {
	values []domain.ID
	calls  int
}

func (generator *explicitRetryIDsFake) New() (domain.ID, error) {
	if generator.calls >= len(generator.values) {
		return "", ErrExplicitRetryInvalid
	}
	value := generator.values[generator.calls]
	generator.calls++
	return value, nil
}

func explicitRetryFixture(t *testing.T) (ExplicitRetryService, *admissionSourceFake, *explicitRetryRepositoryFake, *explicitRetrySettingsFake, *explicitRetryIDsFake, ExplicitRetryRequest) {
	t.Helper()
	admission, source, admissionRequest := validAdmissionTestFixture(t)
	admitted, err := admission.Admit(context.Background(), admissionRequest)
	if err != nil {
		t.Fatal(err)
	}
	source.selections = nil
	parentJobID := domain.ID("018f9e40-0000-7000-8000-000000000270")
	now := time.Unix(1_700_000_000, 0).UTC()
	parent := sharedjob.Record{
		ID: parentJobID, ProjectID: domain.ID(admitted.Input.Base.ProjectID), Kind: AIJobKind, RevisionID: domain.ID(admitted.Input.Base.ConfigRevisionID),
		InputHash: string(admitted.InputHash), IdempotencyKey: "original", RequestHash: string(admitted.InputHash), Status: sharedjob.Failed, CreatedAt: now, UpdatedAt: now,
	}
	manifest := attemptLedgerManifest("018f9e40-0000-7000-8000-000000000271")
	manifest.InputHash = admitted.InputHash
	manifest.EvidenceManifestHash = aicontract.Hash(strings.Repeat("b", 64))
	attempt, err := NewAttemptRecord(parentJobID, AttemptInitial, 1, 0, "", "", manifest)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := NewAttemptOutcomeReceipt(parentJobID, attempt.AttemptID, aicontract.OutcomeFailed, "AI_PROVIDER_TIMEOUT")
	if err != nil {
		t.Fatal(err)
	}
	basis := ExplicitRetryBasis{
		ParentJob: parent, Input: admitted.Input, LastAttempt: attempt, Outcome: outcome,
		Failure: aiprovider.TerminalError{Code: "AI_PROVIDER_TIMEOUT", Class: aiprovider.ErrorTimeout, Retryable: true, Message: "Provider request timed out"},
	}
	repository := &explicitRetryRepositoryFake{basis: basis, records: map[string]ExplicitRetryRecord{}}
	settings := &explicitRetrySettingsFake{capability: aiprovider.CapabilityAvailable}
	ids := &explicitRetryIDsFake{values: []domain.ID{"018f9e40-0000-7000-8000-000000000272", "018f9e40-0000-7000-8000-000000000273"}}
	service := ExplicitRetryService{Admission: admission, Settings: settings, IDs: ids, Repository: repository}
	request := ExplicitRetryRequest{ParentJobID: parentJobID, IdempotencyKey: "explicit-retry-1", UserConfirmed: true}
	return service, source, repository, settings, ids, request
}

func TestExplicitRetryCreatesNewJobAttemptAndPreservesFailedLineage(t *testing.T) {
	service, source, repository, settings, ids, request := explicitRetryFixture(t)
	record, replay, err := service.Retry(context.Background(), request)
	if err != nil || replay || !record.Valid() || record.State.Job.ID == request.ParentJobID || record.Attempt.Kind != AttemptRetry || record.Attempt.ParentJobID != request.ParentJobID || record.Attempt.ParentID != repository.basis.LastAttempt.AttemptID {
		t.Fatalf("record=%#v replay=%v err=%v", record, replay, err)
	}
	if len(source.selections) != 1 || len(settings.calls) != 1 || ids.calls != 2 || repository.loads != 1 || repository.creates != 1 {
		t.Fatalf("rechecks=%d settings=%d ids=%d loads=%d creates=%d", len(source.selections), len(settings.calls), ids.calls, repository.loads, repository.creates)
	}
	if repository.basis.ParentJob.Status != sharedjob.Failed || repository.basis.Outcome.Outcome != aicontract.OutcomeFailed {
		t.Fatal("parent lineage was mutated")
	}

	replayed, replay, err := service.Retry(context.Background(), request)
	if err != nil || !replay || replayed.State.Job.ID != record.State.Job.ID || len(source.selections) != 1 || len(settings.calls) != 1 || ids.calls != 2 || repository.creates != 1 {
		t.Fatalf("replayed=%#v replay=%v err=%v", replayed, replay, err)
	}
}

func TestExplicitRetryRejectsDriftBeforeSettingsOrJobCreation(t *testing.T) {
	for name, mutate := range map[string]func(*admissionSourceFake){
		"graph identity":   func(source *admissionSourceFake) { source.snapshot.Base.GraphContentHash = admissionHash("9") },
		"target version":   func(source *admissionSourceFake) { source.snapshot.Targets[0].EntityVersion++ },
		"required version": func(source *admissionSourceFake) { source.snapshot.RequiredVersions[0].Hash = admissionHash("8") },
	} {
		t.Run(name, func(t *testing.T) {
			service, source, repository, settings, ids, request := explicitRetryFixture(t)
			mutate(source)
			if _, _, err := service.Retry(context.Background(), request); !errors.Is(err, ErrExplicitRetryStale) {
				t.Fatalf("err=%v", err)
			}
			if len(settings.calls) != 0 || ids.calls != 0 || repository.creates != 0 {
				t.Fatalf("settings=%d ids=%d creates=%d", len(settings.calls), ids.calls, repository.creates)
			}
		})
	}
}

func TestExplicitRetryRejectsPermanentNonIdempotentAndUnavailableCapability(t *testing.T) {
	for name, test := range map[string]struct {
		mutate func(*explicitRetryRepositoryFake, *explicitRetrySettingsFake)
		want   error
	}{
		"permanent failure": {func(repository *explicitRetryRepositoryFake, _ *explicitRetrySettingsFake) {
			repository.basis.Failure = aiprovider.TerminalError{Code: "AI_PROVIDER_TIMEOUT", Class: aiprovider.ErrorPermanent, Retryable: false, Message: "Provider rejected the request"}
		}, ErrExplicitRetryNotRetryable},
		"non-idempotent tool": {func(repository *explicitRetryRepositoryFake, _ *explicitRetrySettingsFake) {
			repository.basis.NonIdempotentToolCalls = 1
		}, ErrExplicitRetryNotRetryable},
		"capability unavailable": {func(_ *explicitRetryRepositoryFake, settings *explicitRetrySettingsFake) {
			settings.capability = aiprovider.CapabilityUnavailable
		}, ErrExplicitRetryCapability},
	} {
		t.Run(name, func(t *testing.T) {
			service, _, repository, settings, _, request := explicitRetryFixture(t)
			test.mutate(repository, settings)
			if _, _, err := service.Retry(context.Background(), request); !errors.Is(err, test.want) {
				t.Fatalf("err=%v want=%v", err, test.want)
			}
			if repository.creates != 0 {
				t.Fatalf("created=%d", repository.creates)
			}
		})
	}
}

func TestExplicitRetryRequiresUserConfirmation(t *testing.T) {
	service, source, repository, settings, ids, request := explicitRetryFixture(t)
	request.UserConfirmed = false
	if _, _, err := service.Retry(context.Background(), request); !errors.Is(err, ErrExplicitRetryInvalid) {
		t.Fatalf("err=%v", err)
	}
	if len(source.selections) != 0 || repository.loads != 0 || len(settings.calls) != 0 || ids.calls != 0 {
		t.Fatal("unconfirmed request reached retry work")
	}
}

var _ ExplicitRetryRepository = (*explicitRetryRepositoryFake)(nil)
var _ ExplicitRetrySettingsSource = (*explicitRetrySettingsFake)(nil)
var _ ExplicitRetryIDGenerator = (*explicitRetryIDsFake)(nil)
