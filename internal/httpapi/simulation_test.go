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
	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type simulationRunReaderFake struct {
	run           store.SimulationRun
	metrics       []store.SimulationMetricResult
	verifications []store.SimulationVerification
	readCalls     int
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
	reader := &simulationRunReaderFake{run: store.SimulationRun{ID: id, JobID: jobID, ProjectID: id, RevisionID: revisionID, ScenarioDefinitionID: sceneID, InputHash: hash, FingerprintHash: hash, ResultHash: hash, CanonicalResult: `{"schema":"simulation-result-v1"}`, CreatedAt: now}, metrics: []store.SimulationMetricResult{{MetricID: "metric-dps", MetricVersion: "v1", Status: "available", CanonicalResult: `{"value":"10"}`}}, verifications: []store.SimulationVerification{{ID: verificationID, SourceRunID: id, ReproductionRunID: reproductionID, InputHash: hash, FingerprintHash: hash, SourceResultHash: hash, ReproductionResultHash: hash, Status: "verified", CreatedAt: now}}}
	engine := gin.New()
	NewSimulationHandler(func() simulationRunReader { return reader }).Register(engine)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/simulation-runs/"+string(id), nil))
	if response.Code != http.StatusOK || reader.readCalls != 1 || !strings.Contains(response.Body.String(), `"scenario_definition_id":"`+string(sceneID)+`"`) || !strings.Contains(response.Body.String(), `"metric-dps"`) || !strings.Contains(response.Body.String(), `"verification_refs"`) || !strings.Contains(response.Body.String(), `"reproducible":true`) {
		t.Fatalf("status=%d reads=%d body=%s", response.Code, reader.readCalls, response.Body.String())
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
