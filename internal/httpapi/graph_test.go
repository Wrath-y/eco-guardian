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
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

type graphServiceFake struct {
	job         graphsync.GraphJob
	ensureID    domain.ID
	retryID     domain.ID
	retryOf     domain.ID
	retryKey    string
	ensureErr   error
	ensureCalls int
	retryError  error
	status      app.GraphStatus
	statusErr   error
	events      []graphsync.GraphJobEvent
	cancelled   bool
}

func (f *graphServiceFake) GraphStatus(context.Context, domain.ID) (app.GraphStatus, error) {
	return f.status, f.statusErr
}

func (f *graphServiceFake) EnsureGraphSync(_ context.Context, revisionID domain.ID) (graphsync.GraphJob, bool, error) {
	f.ensureID = revisionID
	f.ensureCalls++
	return f.job, false, f.ensureErr
}
func (f *graphServiceFake) RetryGraphSync(_ context.Context, revisionID, retryOf domain.ID, key string) (graphsync.GraphJob, bool, error) {
	f.retryID, f.retryOf, f.retryKey = revisionID, retryOf, key
	return f.job, false, f.retryError
}
func (f *graphServiceFake) GetGraphJob(_ context.Context, id domain.ID) (graphsync.GraphJob, error) {
	if id != f.job.ID {
		return graphsync.GraphJob{}, app.ErrGraphOperationUnavailable
	}
	return f.job, nil
}
func (f *graphServiceFake) ListGraphJobEvents(_ context.Context, id domain.ID, after int64) ([]graphsync.GraphJobEvent, error) {
	if id != f.job.ID {
		return nil, app.ErrGraphOperationUnavailable
	}
	result := []graphsync.GraphJobEvent{}
	for _, event := range f.events {
		if event.Ordinal > after {
			result = append(result, event)
		}
	}
	return result, nil
}
func (f *graphServiceFake) CancelGraphJob(_ context.Context, id domain.ID) (graphsync.GraphJob, bool, error) {
	if id != f.job.ID {
		return graphsync.GraphJob{}, false, app.ErrGraphOperationUnavailable
	}
	f.cancelled = true
	return f.job, true, nil
}

func TestGraphHandlerEnsuresExactRevisionAndRetriesWithStableKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	revisionID := domain.ID("01948c1e-0000-7000-8000-000000000000")
	jobID := domain.ID("01948c1e-0000-7000-8000-000000000001")
	previousID := domain.ID("01948c1e-0000-7000-8000-000000000002")
	service := &graphServiceFake{job: graphsync.GraphJob{ID: jobID, ProjectID: revisionID, RevisionID: revisionID, InputHash: strings.Repeat("a", 64), IdempotencyKey: "automatic", RequestHash: strings.Repeat("b", 64), Status: graphsync.JobQueued, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}
	engine := gin.New()
	NewGraphHandler(func() app.GraphSyncService { return service }).Register(engine)

	ensure := httptest.NewRecorder()
	ensureRequest := httptest.NewRequest(http.MethodPost, "/api/v1/revisions/"+string(revisionID)+"/graph-sync", strings.NewReader(`{"intent":"ensure"}`))
	ensureRequest.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(ensure, ensureRequest)
	if ensure.Code != http.StatusAccepted || service.ensureID != revisionID || ensure.Header().Get("Location") != "/api/v1/jobs/"+string(jobID) || !strings.Contains(ensure.Body.String(), `"kind":"graph_sync"`) {
		t.Fatalf("ensure status=%d id=%s location=%q body=%s", ensure.Code, service.ensureID, ensure.Header().Get("Location"), ensure.Body.String())
	}
	replay := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodPost, "/api/v1/revisions/"+string(revisionID)+"/graph-sync", strings.NewReader(`{"intent":"ensure"}`))
	replayRequest.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(replay, replayRequest)
	if replay.Code != http.StatusAccepted || service.ensureCalls != 2 || !strings.Contains(replay.Body.String(), `"id":"`+string(jobID)+`"`) {
		t.Fatalf("ensure replay status=%d calls=%d body=%s", replay.Code, service.ensureCalls, replay.Body.String())
	}

	retry := httptest.NewRecorder()
	retryRequest := httptest.NewRequest(http.MethodPost, "/api/v1/revisions/"+string(revisionID)+"/graph-sync", strings.NewReader(`{"intent":"retry","retry_of_job_id":"`+string(previousID)+`"}`))
	retryRequest.Header.Set("Content-Type", "application/json")
	retryRequest.Header.Set("Idempotency-Key", "retry-one")
	engine.ServeHTTP(retry, retryRequest)
	if retry.Code != http.StatusAccepted || service.retryID != revisionID || service.retryOf != previousID || service.retryKey != "retry-one" {
		t.Fatalf("retry status=%d revision=%s previous=%s key=%q", retry.Code, service.retryID, service.retryOf, service.retryKey)
	}
}

func TestGraphHandlerMapsUnknownAndBlockedRevisionsWithoutCreatingJobs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	revisionID := "01948c1e-0000-7000-8000-000000000000"
	for name, testCase := range map[string]struct {
		admissionErr error
		code         string
	}{
		"unknown": {app.ErrGraphRevisionUnavailable, "REVISION_NOT_FOUND"},
		"blocked": {graphsync.ErrValidationNotPassed, "GRAPH_VALIDATION_REQUIRED"},
	} {
		t.Run(name, func(t *testing.T) {
			service := &graphServiceFake{ensureErr: testCase.admissionErr}
			engine := gin.New()
			NewGraphHandler(func() app.GraphSyncService { return service }).Register(engine)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/revisions/"+revisionID+"/graph-sync", strings.NewReader(`{"intent":"ensure"}`))
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(response, request)
			if response.Code == http.StatusAccepted || !strings.Contains(response.Body.String(), `"code":"`+testCase.code+`"`) || service.ensureCalls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, service.ensureCalls, response.Body.String())
			}
		})
	}
	service := &graphServiceFake{statusErr: errors.New("status unavailable")}
	engine := gin.New()
	NewGraphHandler(func() app.GraphSyncService { return service }).Register(engine)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/revisions/"+revisionID+"/graph-status", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"GRAPH_CAPABILITY_UNAVAILABLE"`) {
		t.Fatalf("status read=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGraphHandlerRejectsRetryWithoutIdempotencyKeyAndUnavailableService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	revisionID := "01948c1e-0000-7000-8000-000000000000"
	engine := gin.New()
	NewGraphHandler(func() app.GraphSyncService { return nil }).Register(engine)
	missing := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/revisions/"+revisionID+"/graph-sync", strings.NewReader(`{"intent":"retry","retry_of_job_id":"01948c1e-0000-7000-8000-000000000001"}`))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(missing, request)
	if missing.Code != http.StatusServiceUnavailable || !strings.Contains(missing.Body.String(), `"code":"GRAPH_CAPABILITY_UNAVAILABLE"`) {
		t.Fatalf("missing service status=%d body=%s", missing.Code, missing.Body.String())
	}

	service := &graphServiceFake{}
	engine = gin.New()
	NewGraphHandler(func() app.GraphSyncService { return service }).Register(engine)
	unsafe := httptest.NewRecorder()
	unsafeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/revisions/"+revisionID+"/graph-sync", strings.NewReader(`{"intent":"retry","retry_of_job_id":"01948c1e-0000-7000-8000-000000000001"}`))
	unsafeRequest.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(unsafe, unsafeRequest)
	if unsafe.Code != http.StatusBadRequest || !strings.Contains(unsafe.Body.String(), `"code":"GRAPH_RETRY_NOT_SAFE"`) || service.retryKey != "" {
		t.Fatalf("unsafe retry status=%d key=%q body=%s", unsafe.Code, service.retryKey, unsafe.Body.String())
	}
}

func TestGraphHandlerReadsExactStatusWithoutMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	revisionID := domain.ID("01948c1e-0000-7000-8000-000000000000")
	service := &graphServiceFake{status: app.GraphStatus{RevisionID: string(revisionID), ConfigHash: strings.Repeat("a", 64), Pipeline: graphsync.StateFailed, HasSyncState: true, Freshness: graphsync.Freshness{Reasons: []string{"GRAPH_NOT_READY"}}, Validation: "PASS", Warnings: []string{"DEGRADED_VECTOR"}, SafeError: "PROVIDER_TASK_FAILED", ImpactState: "queued", Provider: &graphsync.Snapshot{Namespace: string(revisionID), Version: string(revisionID), Status: "failed", Components: []graphsync.Component{{Name: "graph", State: "failed"}}}, ProviderObservedAt: time.Now().UTC()}}
	engine := gin.New()
	NewGraphHandler(func() app.GraphSyncService { return service }).Register(engine)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/revisions/"+string(revisionID)+"/graph-status", nil)
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"revision_id":"`+string(revisionID)+`"`) || !strings.Contains(response.Body.String(), `"freshness":"stale"`) || !strings.Contains(response.Body.String(), `"impact_state":"queued"`) || !strings.Contains(response.Body.String(), `"actions":["retry","inspect"]`) || !strings.Contains(response.Body.String(), `"code":"DEGRADED_VECTOR"`) || !strings.Contains(response.Body.String(), `"components":[{"name":"graph","state":"failed"}]`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
