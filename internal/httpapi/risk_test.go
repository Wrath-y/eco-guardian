package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/zouyi/eco-guardian/internal/app"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type reviewServiceFake struct {
	job         sharedjob.Record
	review      riskdto.RiskReview
	events      []sharedjob.Event
	createErr   error
	getErr      error
	cancelErr   error
	createCalls int
	getCalls    int
	cancelCalls int
	eventAfters []int64
	lastKey     string
}

func (s *reviewServiceFake) CreateRiskReview(_ context.Context, _ riskdto.RiskReviewCommand, key string) (sharedjob.Record, error) {
	s.createCalls++
	s.lastKey = key
	return s.job, s.createErr
}

func (s *reviewServiceFake) GetRiskReview(context.Context, domain.ID) (riskdto.RiskReview, error) {
	s.getCalls++
	return s.review, s.getErr
}

func (s *reviewServiceFake) GetRiskJob(context.Context, domain.ID) (sharedjob.Record, error) {
	return s.job, nil
}

func (s *reviewServiceFake) CancelRiskJob(context.Context, domain.ID) (sharedjob.Record, bool, error) {
	s.cancelCalls++
	return s.job, true, s.cancelErr
}

func (s *reviewServiceFake) ListRiskJobEvents(_ context.Context, _ domain.ID, after int64) ([]sharedjob.Event, error) {
	s.eventAfters = append(s.eventAfters, after)
	result := []sharedjob.Event{}
	for _, event := range s.events {
		if event.Ordinal > after {
			result = append(result, event)
		}
	}
	return result, nil
}

func riskHTTPFixture(t *testing.T, status sharedjob.Status) (*reviewServiceFake, []byte) {
	t.Helper()
	raw, err := os.ReadFile("../../api/fixtures/risk-evaluate-request.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	jobID := domain.ID("01948c1e-0000-7000-8000-000000000010")
	revisionID := domain.ID("01948c1e-0000-7000-8000-000000000001")
	reportID := domain.ID("01948c1e-0000-7000-8000-000000000020")
	job := sharedjob.Record{ID: jobID, ProjectID: domain.ID("01948c1e-0000-7000-8000-000000000099"), Kind: "risk_review", RevisionID: revisionID, InputHash: strings.Repeat("a", 64), IdempotencyKey: "risk-http", RequestHash: strings.Repeat("b", 64), Status: status, CreatedAt: now, UpdatedAt: now}
	if status == sharedjob.Succeeded {
		job.Result = &sharedjob.Result{Type: "risk_review", ID: reportID, URL: "/api/v1/risk-reviews/" + string(reportID)}
	}
	events := []sharedjob.Event{
		{JobID: jobID, Ordinal: 1, Phase: "MATERIALIZED", Progress: 10, CreatedAt: now},
		{JobID: jobID, Ordinal: 2, Phase: "SUCCEEDED", Progress: 100, Result: job.Result, CreatedAt: now.Add(time.Second)},
	}
	reviewID := uuid.MustParse(string(reportID))
	return &reviewServiceFake{job: job, review: riskdto.RiskReview{Id: reviewID}, events: events}, raw
}

func TestRiskReviewHTTPAdmitsGeneratedCommandAndReturnsDurableLocation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, raw := riskHTTPFixture(t, sharedjob.Queued)
	engine := gin.New()
	NewRiskReviewHandler(func() ReviewService { return service }).Register(engine)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/risk-reviews", bytes.NewReader(raw))
	request.Header.Set("Idempotency-Key", "risk-http")
	request.Header.Set("X-Request-ID", "request-risk-1")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted || recorder.Header().Get("Location") != "/api/v1/jobs/"+string(service.job.ID) || service.createCalls != 1 || service.lastKey != "risk-http" {
		t.Fatalf("status=%d location=%q calls=%d body=%s", recorder.Code, recorder.Header().Get("Location"), service.createCalls, recorder.Body.String())
	}
	var accepted riskdto.RiskJobAccepted
	if err := json.Unmarshal(recorder.Body.Bytes(), &accepted); err != nil || accepted.Job.Kind != "risk_review" || accepted.Job.Status != "queued" || accepted.Location != recorder.Header().Get("Location") {
		t.Fatalf("accepted=%#v err=%v", accepted, err)
	}
}

func TestRiskReviewHTTPAcceptsBothCommandsBaselineAndThresholdTaggedSelections(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, raw := riskHTTPFixture(t, sharedjob.Queued)
	engine := gin.New()
	NewRiskReviewHandler(func() ReviewService { return service }).Register(engine)
	var evaluate map[string]any
	if err := json.Unmarshal(raw, &evaluate); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("f", 64)
	baseline := cloneRiskPayload(t, evaluate)
	baseline["baseline"] = map[string]any{"type": "BASELINE", "release_id": "01948c1e-0000-7000-8000-000000000030", "revision": map[string]any{"revision_id": "01948c1e-0000-7000-8000-000000000031", "config_hash": hash, "version_manifest_hash": hash}}
	baseline["simulation_runs"] = append(baseline["simulation_runs"].([]any), map[string]any{"run_id": "01948c1e-0000-7000-8000-000000000032", "result_hash": hash})
	starter := cloneRiskPayload(t, evaluate)
	starter["threshold"] = map[string]any{"type": "STARTER", "confirmed": true}
	modified := cloneRiskPayload(t, evaluate)
	modified["threshold"] = map[string]any{"type": "MODIFIED_STARTER", "confirmed": true, "body": map[string]any{
		"schema_version": "v1", "source": "modified-starter", "assumptions": []any{"reviewed locally"},
		"entries":                  []any{map[string]any{"scene_id": "resource-balance", "scene_version": "v1", "metric_id": "metric-resource", "metric_version": "v1", "balance_group": nil, "unit": "ratio", "direction": "target_range", "relative_warning": "0.1", "relative_block": "0.25", "absolute_warning": nil, "absolute_block": nil}},
		"structural_rule_versions": []any{map[string]any{"id": "risk-structure", "version": "v1", "hash": hash}},
	}}
	decision, err := os.ReadFile("../../api/fixtures/risk-decision-request.json")
	if err != nil {
		t.Fatal(err)
	}
	payloads := [][]byte{mustRiskJSON(t, evaluate), mustRiskJSON(t, baseline), mustRiskJSON(t, starter), mustRiskJSON(t, modified), decision}
	for index, payload := range payloads {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/risk-reviews", bytes.NewReader(payload))
		request.Header.Set("Idempotency-Key", "tagged-"+string(rune('a'+index)))
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("case=%d status=%d body=%s", index, recorder.Code, recorder.Body.String())
		}
	}
	if service.createCalls != len(payloads) {
		t.Fatalf("create calls=%d", service.createCalls)
	}
}

func cloneRiskPayload(t *testing.T, source map[string]any) map[string]any {
	t.Helper()
	raw := mustRiskJSON(t, source)
	var copy map[string]any
	if err := json.Unmarshal(raw, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

func mustRiskJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRiskReviewHTTPRejectsClientOutcomesAndSanitizesFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, raw := riskHTTPFixture(t, sharedjob.Queued)
	engine := gin.New()
	NewRiskReviewHandler(func() ReviewService { return service }).Register(engine)

	cases := []struct {
		name       string
		body       []byte
		key        string
		serviceErr error
		wantStatus int
		wantCode   string
	}{
		{"missing idempotency", raw, "", nil, 400, "RISK_IDEMPOTENCY_REQUIRED"},
		{"client severity", bytes.Replace(raw, []byte(`"command": "evaluate",`), []byte(`"command": "evaluate", "severity": "PASS",`), 1), "risk-http", nil, 400, "RISK_COMMAND_INVALID"},
		{"conflict", raw, "risk-http", store.ErrJobIdempotencyConflict, 409, "IDEMPOTENCY_CONFLICT"},
		{"sanitized storage", raw, "risk-http", errors.New("sqlite /Users/private SELECT secret stack"), 500, "STORAGE_FAILURE"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			service.createErr = test.serviceErr
			request := httptest.NewRequest(http.MethodPost, "/api/v1/risk-reviews", bytes.NewReader(test.body))
			if test.key != "" {
				request.Header.Set("Idempotency-Key", test.key)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || !strings.Contains(recorder.Body.String(), `"code":"`+test.wantCode+`"`) || recorder.Header().Get("X-Request-ID") == "" {
				t.Fatalf("status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			lower := strings.ToLower(recorder.Body.String())
			for _, unsafe := range []string{"sqlite", "/users/", "select ", "secret", "stack"} {
				if strings.Contains(lower, unsafe) {
					t.Fatalf("unsafe detail leaked: %s", recorder.Body.String())
				}
			}
		})
	}
}

func TestRiskProblemCatalogOverridesUntrustedServiceTitles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, raw := riskHTTPFixture(t, sharedjob.Queued)
	service.createErr = NewRiskAPIError(418, "RISK_BASELINE_INVALID", "sqlite /Users/private secret", true)
	engine := gin.New()
	NewRiskReviewHandler(func() ReviewService { return service }).Register(engine)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/risk-reviews", bytes.NewReader(raw))
	request.Header.Set("Idempotency-Key", "catalog")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != 409 || !strings.Contains(recorder.Body.String(), "Risk baseline identity is invalid") || strings.Contains(strings.ToLower(recorder.Body.String()), "sqlite") || strings.Contains(strings.ToLower(recorder.Body.String()), "/users/") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRiskProblemCatalogMatchesFrozenOpenAPIFixtures(t *testing.T) {
	raw, err := os.ReadFile("../../api/fixtures/problems.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []riskdto.Problem
	if err = json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	frozen := make(map[string]riskdto.Problem, len(fixtures))
	for _, fixture := range fixtures {
		frozen[string(fixture.Code)] = fixture
	}
	for code, value := range riskProblemCatalog {
		fixture, found := frozen[code]
		if !found {
			t.Fatalf("risk problem %s has no frozen fixture", code)
		}
		if fixture.Status != value.Status || fixture.Title != value.Title || fixture.Retryable != value.Retryable {
			t.Errorf("%s catalog=%#v fixture=%#v", code, value, fixture)
		}
	}
}

func TestRiskReviewHTTPImmutableGetAndProjectBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _ := riskHTTPFixture(t, sharedjob.Succeeded)
	service.review.ReadTime = riskdto.RiskReadTimeProjection{Freshness: "stale", FreshnessReasons: []string{"THRESHOLD_CHANGED"}, GateState: "STALE", GateItemIds: []string{}, ImpactEvidenceRefs: []riskdto.RiskIdentity{}}
	engine := gin.New()
	NewRiskReviewHandler(func() ReviewService { return service }).Register(engine)
	id := service.job.Result.ID
	first := httptest.NewRecorder()
	engine.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/v1/risk-reviews/"+string(id), nil))
	second := httptest.NewRecorder()
	engine.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/v1/risk-reviews/"+string(id), nil))
	if first.Code != 200 || first.Body.String() != second.Body.String() || service.getCalls != 2 || !strings.Contains(first.Body.String(), `"freshness":"stale"`) || !strings.Contains(first.Body.String(), `"gate_state":"STALE"`) {
		t.Fatalf("first=%d second=%d calls=%d", first.Code, second.Code, service.getCalls)
	}

	closed := gin.New()
	NewRiskReviewHandler(func() ReviewService { return nil }).Register(closed)
	recorder := httptest.NewRecorder()
	closed.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/risk-reviews/"+string(id), nil))
	if recorder.Code != 404 || !strings.Contains(recorder.Body.String(), `"code":"PROJECT_NOT_OPEN"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRiskReviewHTTPBoundsImmutableResultPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _ := riskHTTPFixture(t, sharedjob.Succeeded)
	service.review.Threshold.Assumptions = []string{strings.Repeat("x", maxRiskReviewResponseBytes)}
	engine := gin.New()
	NewRiskReviewHandler(func() ReviewService { return service }).Register(engine)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/risk-reviews/"+string(service.job.Result.ID), nil))
	if recorder.Code != 500 || !strings.Contains(recorder.Body.String(), `"code":"STORAGE_FAILURE"`) || len(recorder.Body.Bytes()) > 2048 {
		t.Fatalf("status=%d response-bytes=%d body=%s", recorder.Code, len(recorder.Body.Bytes()), recorder.Body.String())
	}
}

func TestRiskJobsUseSharedPollingSSEResumeAndCancelWithoutReadmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service, _ := riskHTTPFixture(t, sharedjob.Succeeded)
	engine := gin.New()
	versions := NewVersionHandler(func() app.VersioningService { return &fakeVersionService{jobErr: errors.New("not a release job")} })
	versions.RegisterDurableResolver(RiskJobResolver(func() ReviewService { return service }))
	versions.Register(engine)
	id := string(service.job.ID)

	poll := httptest.NewRecorder()
	engine.ServeHTTP(poll, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+id, nil))
	if poll.Code != 200 || !strings.Contains(poll.Body.String(), `"latest_event_ordinal":2`) || !strings.Contains(poll.Body.String(), `"result_url":"/api/v1/risk-reviews/`) {
		t.Fatalf("poll status=%d body=%s", poll.Code, poll.Body.String())
	}

	streamRequest := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+id+"/events", nil)
	streamRequest.Header.Set("Last-Event-ID", "1")
	stream := httptest.NewRecorder()
	engine.ServeHTTP(stream, streamRequest)
	if stream.Code != 200 || strings.Contains(stream.Body.String(), "id: 1\n") || !strings.Contains(stream.Body.String(), "id: 2\n") || !strings.Contains(stream.Body.String(), "event: terminal") {
		t.Fatalf("stream status=%d body=%s", stream.Code, stream.Body.String())
	}
	resumed := false
	for _, after := range service.eventAfters {
		resumed = resumed || after == 1
	}
	if service.createCalls != 0 || !resumed {
		t.Fatalf("create calls=%d event afters=%v", service.createCalls, service.eventAfters)
	}

	service.job.Status = sharedjob.Running
	service.job.Result = nil
	cancel := httptest.NewRecorder()
	engine.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+id+"/cancel", nil))
	if cancel.Code != http.StatusAccepted || service.cancelCalls != 1 {
		t.Fatalf("cancel status=%d calls=%d body=%s", cancel.Code, service.cancelCalls, cancel.Body.String())
	}
}
