package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/app"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	"github.com/zouyi/eco-guardian/internal/simulation/metric"
	"github.com/zouyi/eco-guardian/internal/simulation/orchestration"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type simulationRunReaderFake struct {
	run             store.SimulationRun
	metrics         []store.SimulationMetricResult
	verifications   []store.SimulationVerification
	materialization contract.JobMaterialization
	readCalls       int
}

type simulationJobReaderFake struct {
	job       sharedjob.Record
	events    []sharedjob.Event
	cancelled bool
}

func (f *simulationJobReaderFake) ProjectID() domain.ID { return f.job.ProjectID }

func (f *simulationJobReaderFake) GetJob(_ context.Context, id domain.ID) (sharedjob.Record, error) {
	if id != f.job.ID {
		return sharedjob.Record{}, store.ErrJobNotFound
	}
	return f.job, nil
}

func (f *simulationJobReaderFake) RequestCancellation(_ context.Context, id domain.ID) (sharedjob.Record, bool, error) {
	if id != f.job.ID {
		return sharedjob.Record{}, false, store.ErrJobNotFound
	}
	f.cancelled = true
	f.job.Status = sharedjob.Canceled
	return f.job, false, nil
}

func (f *simulationJobReaderFake) ListEvents(_ context.Context, id domain.ID, after int64) ([]sharedjob.Event, error) {
	if id != f.job.ID {
		return nil, store.ErrJobNotFound
	}
	result := []sharedjob.Event{}
	for _, event := range f.events {
		if event.Ordinal > after {
			result = append(result, event)
		}
	}
	return result, nil
}

func (f *simulationRunReaderFake) GetSimulationRun(_ context.Context, id domain.ID) (store.SimulationRun, []store.SimulationMetricResult, error) {
	f.readCalls++
	if id != f.run.ID {
		return store.SimulationRun{}, nil, store.ErrNotFound
	}
	return f.run, f.metrics, nil
}

func (f *simulationRunReaderFake) ListSimulationVerifications(_ context.Context, id domain.ID) ([]store.SimulationVerification, error) {
	if id != f.run.ID {
		return nil, errors.New("unexpected verification lookup")
	}
	return f.verifications, nil
}

func (f *simulationRunReaderFake) GetSimulationJobMaterialization(_ context.Context, id domain.ID) (contract.JobMaterialization, error) {
	if id != f.materialization.JobID {
		return contract.JobMaterialization{}, store.ErrNotFound
	}
	return f.materialization, nil
}

func TestSimulationHandlerReadsImmutableRunWithoutMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := domain.ID("01948c1e-0000-7000-8000-000000000000")
	jobID := domain.ID("01948c1e-0000-7000-8000-000000000001")
	revisionID := domain.ID("01948c1e-0000-7000-8000-000000000002")
	sceneID := domain.ID("01948c1e-0000-7000-8000-000000000003")
	verificationID := domain.ID("01948c1e-0000-7000-8000-000000000004")
	reproductionID := domain.ID("01948c1e-0000-7000-8000-000000000005")
	hash := strings.Repeat("a", 64)
	now := time.Date(2026, 8, 21, 8, 0, 0, 0, time.UTC)
	input := contract.SimulationInputV1{SchemaVersion: contract.SimulationInputSchemaV1, ProjectID: contract.ID(id), RevisionID: contract.ID(revisionID), ConfigHash: hash, ManifestHash: hash, SceneID: "single-target-30s", SceneVersion: "v1", SceneBodyHash: hash, SampleCount: 1, Metrics: []contract.MetricIdentity{{ID: "metric-dps", Version: "v1"}}}
	canonical, err := input.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	reader := &simulationRunReaderFake{run: store.SimulationRun{ID: id, JobID: jobID, ProjectID: id, RevisionID: revisionID, ScenarioDefinitionID: sceneID, InputHash: hash, FingerprintHash: hash, ResultHash: hash, CanonicalResult: `{"schema":"simulation-result-v1"}`, CreatedAt: now, Implementations: contract.V1Descriptors()}, metrics: []store.SimulationMetricResult{{MetricID: "metric-dps", MetricVersion: "v1", Status: "available", CanonicalResult: `{"id":"metric-dps","version":"v1","status":"available","unit":"points_per_second","direction":"higher_is_risk","value":"10","confidence_low":"9","confidence_high":"11","sample_count":1,"assumptions":["independent_samples"]}`}}, verifications: []store.SimulationVerification{{ID: verificationID, SourceRunID: id, ReproductionRunID: reproductionID, InputHash: hash, FingerprintHash: hash, SourceResultHash: hash, ReproductionResultHash: hash, Status: "verified", CreatedAt: now}}, materialization: contract.JobMaterialization{JobID: jobID, ProjectID: id, RevisionID: revisionID, ScenarioDefinitionID: sceneID, CanonicalInput: canonical, InputHash: hash, FingerprintHash: hash, CreatedAt: now}}
	engine := gin.New()
	NewSimulationHandler(func() simulationRunReader { return reader }).Register(engine)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/simulation-runs/"+string(id), nil))
	if response.Code != http.StatusOK || reader.readCalls != 1 || !strings.Contains(response.Body.String(), `"scenario_definition_id":"`+string(sceneID)+`"`) || !strings.Contains(response.Body.String(), `"metric-dps"`) || !strings.Contains(response.Body.String(), `"confidence_low":"9"`) || !strings.Contains(response.Body.String(), `"verification_refs"`) || !strings.Contains(response.Body.String(), `"reproducible":true`) {
		t.Fatalf("status=%d reads=%d body=%s", response.Code, reader.readCalls, response.Body.String())
	}
}

func TestSimulationMetricJSONProjectsImmutableTargetRange(t *testing.T) {
	stored := store.SimulationMetricResult{MetricID: "metric-resource", MetricVersion: "v1", Status: "available", CanonicalResult: `{"id":"metric-resource","version":"v1","status":"available","unit":"ratio","direction":"target_range","target_range":{"lower":"0.8","upper":"1.2","bounds":"inclusive"},"absolute_threshold":null,"value":"1","confidence_low":"0.9","confidence_high":"1.1","sample_count":1000,"assumptions":["independent_samples"]}`}
	value, err := simulationMetricJSON(stored)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := value["target_range"].(*metric.CanonicalValueRange)
	if !ok || target.Lower != "0.8" || target.Upper != "1.2" || target.Bounds != metric.Inclusive || value["absolute_threshold"] != (*string)(nil) {
		t.Fatalf("projection=%#v", value)
	}
}

func TestSimulationHandlerPreservesHistoricalRunWhenCompatibilityIsMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := domain.ID("01948c1e-0000-7000-8000-000000000000")
	hash := strings.Repeat("a", 64)
	reader := &simulationRunReaderFake{run: store.SimulationRun{ID: id, JobID: domain.ID("01948c1e-0000-7000-8000-000000000001"), ProjectID: id, RevisionID: domain.ID("01948c1e-0000-7000-8000-000000000002"), ScenarioDefinitionID: domain.ID("01948c1e-0000-7000-8000-000000000003"), InputHash: hash, FingerprintHash: hash, ResultHash: hash, CanonicalResult: `{"stored":true}`, CreatedAt: time.Now().UTC()}}
	engine := gin.New()
	NewSimulationHandler(func() simulationRunReader { return reader }).Register(engine)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/simulation-runs/"+string(id), nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"canonical_result":"{\"stored\":true}"`) || !strings.Contains(response.Body.String(), `"reproducible":false`) || !strings.Contains(response.Body.String(), "simulation compatibility history is required") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSimulationHandlerScopesMissingAndUnavailableReaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := "01948c1e-0000-7000-8000-000000000000"
	engine := gin.New()
	NewSimulationHandler(func() simulationRunReader { return nil }).Register(engine)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/simulation-runs/"+id, nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"SIMULATION_CAPABILITY_UNAVAILABLE"`) {
		t.Fatalf("unavailable status=%d body=%s", response.Code, response.Body.String())
	}

	missing := &simulationRunReaderFake{}
	engine = gin.New()
	NewSimulationHandler(func() simulationRunReader { return missing }).Register(engine)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/simulation-runs/"+id, nil))
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"SIMULATION_RUN_NOT_FOUND"`) {
		t.Fatalf("missing status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSimulationJobResolverUsesSharedJobRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := domain.ID("01948c1e-0000-7000-8000-000000000000")
	revisionID := domain.ID("01948c1e-0000-7000-8000-000000000001")
	now := time.Now().UTC()
	reader := &simulationJobReaderFake{job: sharedjob.Record{ID: id, ProjectID: id, Kind: "simulation", RevisionID: revisionID, InputHash: strings.Repeat("a", 64), IdempotencyKey: "simulation", RequestHash: strings.Repeat("a", 64), Status: sharedjob.Running, CreatedAt: now, UpdatedAt: now}, events: []sharedjob.Event{{JobID: id, Ordinal: 2, Phase: "SAMPLES_RUNNING", Progress: 50, CreatedAt: now}}}
	handler := NewVersionHandler(func() app.VersioningService { return &fakeVersionService{jobErr: errors.New("not a release job")} })
	handler.RegisterDurableResolver(SimulationJobResolver(func() simulationJobReader { return reader }))
	engine := gin.New()
	handler.Register(engine)

	get := httptest.NewRecorder()
	engine.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+string(id), nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"kind":"simulation"`) || !strings.Contains(get.Body.String(), `"input_hash"`) {
		t.Fatalf("get=%d body=%s", get.Code, get.Body.String())
	}
	cancel := httptest.NewRecorder()
	engine.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+string(id)+"/cancel", nil))
	if cancel.Code != http.StatusAccepted || !reader.cancelled || !strings.Contains(cancel.Body.String(), `"status":"canceled"`) {
		t.Fatalf("cancel=%d canceled=%v body=%s", cancel.Code, reader.cancelled, cancel.Body.String())
	}
	stream := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+string(id)+"/events", nil)
	request.Header.Set("Last-Event-ID", "1")
	engine.ServeHTTP(stream, request)
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "id: 2") || !strings.Contains(stream.Body.String(), "SAMPLES_RUNNING") || !strings.Contains(stream.Body.String(), "event: terminal") {
		t.Fatalf("stream=%d body=%s", stream.Code, stream.Body.String())
	}
}

type simulationAdmissionServiceFake struct {
	result app.SimulationAdmissionResult
	err    error
	seen   app.SimulationAdmission
}

func (f *simulationAdmissionServiceFake) AdmitSimulation(_ context.Context, admission app.SimulationAdmission) (app.SimulationAdmissionResult, error) {
	f.seen = admission
	return f.result, f.err
}

func TestSimulationAdmissionHandlerCreatesSharedJobWithStableLocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projectID := domain.ID("01948c1e-0000-7000-8000-000000000000")
	jobID := domain.ID("01948c1e-0000-7000-8000-000000000001")
	now := time.Now().UTC()
	jobs := &simulationJobReaderFake{job: sharedjob.Record{ID: jobID, ProjectID: projectID, Kind: "simulation", RevisionID: domain.ID("01948c1e-0000-7000-8000-000000000002"), InputHash: strings.Repeat("a", 64), IdempotencyKey: "key", RequestHash: strings.Repeat("a", 64), Status: sharedjob.Queued, CreatedAt: now, UpdatedAt: now}}
	service := &simulationAdmissionServiceFake{result: app.SimulationAdmissionResult{Job: jobID}}
	engine := gin.New()
	NewSimulationAdmissionHandler(func() app.SimulationAdmissionService { return service }, func() simulationJobReader { return jobs }).Register(engine)
	response := httptest.NewRecorder()
	verifyID := domain.ID("01948c1e-0000-7000-8000-000000000003")
	request := httptest.NewRequest(http.MethodPost, "/api/v1/simulation-jobs", strings.NewReader(`{"source":{"revision_id":"01948c1e-0000-7000-8000-000000000002"},"scene_id":"single-target-30s","scene_version":"v1","metrics":[{"id":"metric-dps","version":"v1"}],"sample_count":1,"budget":{"max_events":50},"verify_run_id":"`+string(verifyID)+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "key")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Location") != "/api/v1/jobs/"+string(jobID) || service.seen.ProjectID != projectID || service.seen.IdempotencyKey != "key" || service.seen.VerifyRunID != verifyID || service.seen.Budget == nil || service.seen.Budget.MaxEvents == nil || *service.seen.Budget.MaxEvents != 50 || !strings.Contains(response.Body.String(), `"kind":"simulation"`) {
		t.Fatalf("status=%d location=%q admission=%#v body=%s", response.Code, response.Header().Get("Location"), service.seen, response.Body.String())
	}
}

func TestSimulationAdmissionHandlerRejectsUnsupportedAndBlockedInputs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projectID := domain.ID("01948c1e-0000-7000-8000-000000000000")
	jobs := &simulationJobReaderFake{job: sharedjob.Record{ProjectID: projectID}}
	service := &simulationAdmissionServiceFake{}
	engine := gin.New()
	NewSimulationAdmissionHandler(func() app.SimulationAdmissionService { return service }, func() simulationJobReader { return jobs }).Register(engine)
	for name, test := range map[string]struct {
		err  error
		code string
	}{
		"blocked":        {orchestration.ErrFullValidationRequired, "SIMULATION_VALIDATION_REQUIRED"},
		"source":         {contract.ErrSourceInvalid, "SIMULATION_SOURCE_INVALID"},
		"scene":          {app.ErrSimulationSceneInvalid, "SIMULATION_SCENE_INVALID"},
		"parameter":      {app.ErrSimulationParameterInvalid, "SIMULATION_PARAMETER_INVALID"},
		"metric":         {app.ErrSimulationMetricInvalid, "SIMULATION_METRIC_INVALID"},
		"sample":         {app.ErrSimulationSampleInvalid, "SIMULATION_SAMPLE_INVALID"},
		"budget":         {app.ErrSimulationBudgetInvalid, "SIMULATION_BUDGET_INVALID"},
		"implementation": {app.ErrSimulationImplementationUnavailable, "SIMULATION_IMPLEMENTATION_UNAVAILABLE"},
		"verification":   {app.ErrSimulationVerificationTargetInvalid, "SIMULATION_VERIFICATION_TARGET_INVALID"},
		"idempotency":    {store.ErrJobIdempotencyConflict, "IDEMPOTENCY_CONFLICT"},
	} {
		t.Run(name, func(t *testing.T) {
			service.err = test.err
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/simulation-jobs", strings.NewReader(`{"source":{"revision_id":"01948c1e-0000-7000-8000-000000000002"},"scene_id":"scene","scene_version":"v1","metrics":[{"id":"metric-dps","version":"v1"}]}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "key")
			engine.ServeHTTP(response, request)
			if response.Code == http.StatusAccepted || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) || strings.Contains(response.Body.String(), test.err.Error()) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestSimulationAdmissionHandlerRejectsInvalidSeedBeforeAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projectID := domain.ID("01948c1e-0000-7000-8000-000000000000")
	jobs := &simulationJobReaderFake{job: sharedjob.Record{ProjectID: projectID}}
	service := &simulationAdmissionServiceFake{}
	engine := gin.New()
	NewSimulationAdmissionHandler(func() app.SimulationAdmissionService { return service }, func() simulationJobReader { return jobs }).Register(engine)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/simulation-jobs", strings.NewReader(`{"source":{"revision_id":"01948c1e-0000-7000-8000-000000000002"},"scene_id":"scene","scene_version":"v1","metrics":[{"id":"metric-dps","version":"v1"}],"seed":"not-a-seed"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "key")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"SIMULATION_SEED_INVALID"`) || service.seen.IdempotencyKey != "" {
		t.Fatalf("status=%d admission=%#v body=%s", response.Code, service.seen, response.Body.String())
	}
}
