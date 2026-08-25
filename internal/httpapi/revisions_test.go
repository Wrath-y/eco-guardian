package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/app"
	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	versioninggate "github.com/zouyi/eco-guardian/internal/versioning/gate"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
)

type fakeVersionService struct {
	app.VersioningService
	policies    versioningpolicy.Page
	capability  versioninggate.ReleaseCapability
	created     versioningrelease.Command
	createdJob  versioningrelease.Job
	createErr   error
	createCalls int
	job         versioningrelease.Job
	jobErr      error
	events      []versioningrelease.Event
}

func (f *fakeVersionService) ListPolicies(context.Context, string, int) (versioningpolicy.Page, error) {
	return f.policies, nil
}
func (f *fakeVersionService) ReleaseCapability(context.Context) (versioninggate.ReleaseCapability, error) {
	return f.capability, nil
}
func (f *fakeVersionService) GraphRuntimeCapability(context.Context) app.GraphRuntimeStatus {
	return app.GraphRuntimeStatus{RequiredCapabilities: []string{"snapshot_lifecycle", "task_polling", "activation", "core_graph_query", "bm25"}, Degradations: []string{}, Reasons: []string{"GRAPH_PROVIDER_NOT_CONFIGURED"}}
}
func (f *fakeVersionService) CreateRelease(_ context.Context, command versioningrelease.Command) (versioningrelease.Job, error) {
	f.created, f.createCalls = command, f.createCalls+1
	return f.createdJob, f.createErr
}
func (f *fakeVersionService) GetReleaseJob(context.Context, domain.ID) (versioningrelease.Job, error) {
	return f.job, f.jobErr
}
func (f *fakeVersionService) ListReleaseJobEvents(context.Context, domain.ID, int64) ([]versioningrelease.Event, error) {
	return f.events, nil
}
func (f *fakeVersionService) CancelReleaseJob(context.Context, domain.ID) (versioningrelease.Job, bool, error) {
	return versioningrelease.Job{}, false, errors.New("not a release job")
}

func TestVersionHandlerRejectsAbsentProjectAndInvalidListLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewVersionHandler(func() app.VersioningService { return nil }).Register(engine)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/revisions", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("absent project status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestVersionHandlerDelegatesPolicyCapabilityAndReleaseCommands(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	service := &fakeVersionService{
		policies:   versioningpolicy.Page{Items: []versioningpolicy.ReleasePolicy{{ID: id, DisplayVersion: 1, CanonicalHash: strings.Repeat("a", 64)}}},
		capability: versioninggate.ReleaseCapability{Enabled: false, Reasons: []versioninggate.DisabledReason{{CapabilityID: "graph", GateID: "projection", Reason: "required gate is unregistered"}}},
		createdJob: versioningrelease.Job{ID: jobID},
	}
	engine := gin.New()
	NewVersionHandler(func() app.VersioningService { return service }).Register(engine)

	policyResponse := httptest.NewRecorder()
	engine.ServeHTTP(policyResponse, httptest.NewRequest(http.MethodGet, "/api/v1/release-policies", nil))
	if policyResponse.Code != http.StatusOK || !strings.Contains(policyResponse.Body.String(), "policy_hash") {
		t.Fatalf("policy response=%d body=%s", policyResponse.Code, policyResponse.Body.String())
	}
	capabilityResponse := httptest.NewRecorder()
	engine.ServeHTTP(capabilityResponse, httptest.NewRequest(http.MethodGet, "/api/v1/runtime/capabilities", nil))
	if capabilityResponse.Code != http.StatusOK || !strings.Contains(capabilityResponse.Body.String(), "MISSING") || !strings.Contains(capabilityResponse.Body.String(), "GRAPH_PROVIDER_NOT_CONFIGURED") || !strings.Contains(capabilityResponse.Body.String(), "AI_PROVIDER_UNCONFIGURED") || !strings.Contains(capabilityResponse.Body.String(), "max_format_repairs") {
		t.Fatalf("capability response=%d body=%s", capabilityResponse.Code, capabilityResponse.Body.String())
	}

	body := `{"candidate_revision_id":"` + string(id) + `","config_hash":"` + strings.Repeat("b", 64) + `","version_manifest_hash":"` + strings.Repeat("c", 64) + `","policy_id":"` + string(id) + `","expected_baseline_release_id":null,"confirmations":[]}`
	releaseRequest := httptest.NewRequest(http.MethodPost, "/api/v1/releases", strings.NewReader(body))
	releaseRequest.Header.Set("Content-Type", "application/json")
	releaseRequest.Header.Set("Idempotency-Key", "release-one")
	releaseResponse := httptest.NewRecorder()
	engine.ServeHTTP(releaseResponse, releaseRequest)
	if releaseResponse.Code != http.StatusAccepted || service.createCalls != 1 || !service.created.BaselineSpecified || service.created.IdempotencyKey != "release-one" {
		t.Fatalf("release response=%d command=%#v calls=%d body=%s", releaseResponse.Code, service.created, service.createCalls, releaseResponse.Body.String())
	}
	var accepted map[string]string
	if err := json.Unmarshal(releaseResponse.Body.Bytes(), &accepted); err != nil || accepted["location"] != "/api/v1/jobs/"+string(jobID) {
		t.Fatalf("accepted=%v err=%v", accepted, err)
	}
}

func TestUnavailableAICapabilityDoesNotChangeReleaseOrGraphCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeVersionService{capability: versioninggate.ReleaseCapability{Enabled: true}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/runtime/status", nil)

	baselineEngine := gin.New()
	NewVersionHandler(func() app.VersioningService { return service }).Register(baselineEngine)
	baselineResponse := httptest.NewRecorder()
	baselineEngine.ServeHTTP(baselineResponse, request)

	failedEngine := gin.New()
	handler := NewVersionHandler(func() app.VersioningService { return service })
	handler.RegisterAICapabilityProvider(func(context.Context) aiprovider.Capability {
		return aiprovider.Capability{
			State: aiprovider.CapabilityUnavailable, Enabled: true,
			CredentialPresent: true, Reasons: []string{aiprovider.ReasonProviderUnavailable},
		}
	})
	handler.Register(failedEngine)
	failedResponse := httptest.NewRecorder()
	failedEngine.ServeHTTP(failedResponse, httptest.NewRequest(http.MethodGet, "/api/v1/runtime/status", nil))

	var baseline, failed map[string]any
	if err := json.Unmarshal(baselineResponse.Body.Bytes(), &baseline); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(failedResponse.Body.Bytes(), &failed); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseline["release"], failed["release"]) || !reflect.DeepEqual(baseline["graph"], failed["graph"]) {
		t.Fatalf("AI failure changed deterministic capabilities: baseline=%v failed=%v", baseline, failed)
	}
	ai := failed["ai"].(map[string]any)
	if ai["state"] != "unavailable" || !strings.Contains(failedResponse.Body.String(), aiprovider.ReasonProviderUnavailable) {
		t.Fatalf("AI failure was not isolated: %s", failedResponse.Body.String())
	}
}

func TestRuntimeStatusProjectsMissingAndIncompatibleAIReasons(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeVersionService{capability: versioninggate.ReleaseCapability{Enabled: true}}
	for _, capability := range []aiprovider.Capability{
		{State: aiprovider.CapabilityUnconfigured, Enabled: true, Reasons: []string{aiprovider.ReasonCredentialRequired}},
		{State: aiprovider.CapabilityUnavailable, Enabled: true, CredentialPresent: true, StructuredOutput: true, Streaming: true, Reasons: []string{aiprovider.ReasonToolCallsUnsupported}},
	} {
		engine := gin.New()
		handler := NewVersionHandler(func() app.VersioningService { return service })
		projected := capability
		handler.RegisterAICapabilityProvider(func(context.Context) aiprovider.Capability { return projected })
		handler.Register(engine)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/runtime/status", nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), capability.Reasons[0]) || !strings.Contains(response.Body.String(), `"release":{"disabled_reasons":[],"enabled":true}`) {
			t.Fatalf("runtime capability response=%d body=%s", response.Code, response.Body.String())
		}
	}
}

func TestVersionHandlerRejectsInvalidIdempotencyKeyAndReadOnlyMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeVersionService{}
	engine := gin.New()
	NewVersionHandler(func() app.VersioningService { return service }).Register(engine)
	invalid := httptest.NewRequest(http.MethodPost, "/api/v1/releases", strings.NewReader(`{"expected_baseline_release_id":null}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalid.Header.Set("Idempotency-Key", " ")
	invalidResponse := httptest.NewRecorder()
	engine.ServeHTTP(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest || service.createCalls != 0 {
		t.Fatalf("invalid release response=%d calls=%d body=%s", invalidResponse.Code, service.createCalls, invalidResponse.Body.String())
	}
	readonlyResponse := httptest.NewRecorder()
	engine.ServeHTTP(readonlyResponse, httptest.NewRequest(http.MethodDelete, "/api/v1/releases/any", nil))
	if readonlyResponse.Code != http.StatusMethodNotAllowed || !strings.Contains(readonlyResponse.Body.String(), "REVISION_IMMUTABLE") {
		t.Fatalf("read-only response=%d body=%s", readonlyResponse.Code, readonlyResponse.Body.String())
	}
}

func TestVersionHandlerMapsReleaseBaseConflictToProblemDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	service := &fakeVersionService{createErr: versioningrelease.ErrReleaseBaseConflict}
	engine := gin.New()
	NewVersionHandler(func() app.VersioningService { return service }).Register(engine)
	body := `{"candidate_revision_id":"` + string(id) + `","config_hash":"` + strings.Repeat("b", 64) + `","version_manifest_hash":"` + strings.Repeat("c", 64) + `","policy_id":"` + string(id) + `","expected_baseline_release_id":null,"confirmations":[{"kind":"establish_baseline","confirmed":true}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/releases", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "release-conflict")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "RELEASE_BASE_CONFLICT") {
		t.Fatalf("response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestVersionHandlerResumesPersistedJobEventsAndCompletesTerminalStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	service := &fakeVersionService{job: versioningrelease.Job{ID: id, Status: versioningrelease.JobSucceeded}, events: []versioningrelease.Event{{JobID: id, Ordinal: 2, Phase: "SUCCEEDED", Progress: 100}}}
	engine := gin.New()
	NewVersionHandler(func() app.VersioningService { return service }).Register(engine)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+string(id)+"/events", nil)
	request.Header.Set("Last-Event-ID", "1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "id: 2") || !strings.Contains(response.Body.String(), "event: terminal") {
		t.Fatalf("stream response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestVersionHandlerServesGraphJobsThroughSharedJobRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	revisionID := domain.ID("01948c1e-0000-7000-8000-000000000000")
	jobID := domain.ID("01948c1e-0000-7000-8000-000000000001")
	graph := &graphServiceFake{job: graphsync.GraphJob{ID: jobID, ProjectID: revisionID, RevisionID: revisionID, InputHash: strings.Repeat("a", 64), IdempotencyKey: "automatic", RequestHash: strings.Repeat("b", 64), Status: graphsync.JobFailed, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, events: []graphsync.GraphJobEvent{{JobID: jobID, Ordinal: 2, Phase: graphsync.PhaseProjected, Progress: 40, SafeError: "PROVIDER_TASK_FAILED"}}}
	versions := &fakeVersionService{jobErr: errors.New("not a release job")}
	engine := gin.New()
	NewVersionHandlerWithGraph(func() app.VersioningService { return versions }, func() app.GraphSyncService { return graph }).Register(engine)

	job := httptest.NewRecorder()
	engine.ServeHTTP(job, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+string(jobID), nil))
	if job.Code != http.StatusOK || !strings.Contains(job.Body.String(), `"kind":"graph_sync"`) {
		t.Fatalf("graph job=%d body=%s", job.Code, job.Body.String())
	}

	stream := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+string(jobID)+"/events", nil)
	request.Header.Set("Last-Event-ID", "1")
	engine.ServeHTTP(stream, request)
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "id: 2") || !strings.Contains(stream.Body.String(), `"ordinal":2`) || !strings.Contains(stream.Body.String(), "event: terminal") {
		t.Fatalf("graph stream=%d body=%s", stream.Code, stream.Body.String())
	}

	cancel := httptest.NewRecorder()
	engine.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+string(jobID)+"/cancel", nil))
	if cancel.Code != http.StatusAccepted || !graph.cancelled || !strings.Contains(cancel.Body.String(), `"kind":"graph_sync"`) {
		t.Fatalf("graph cancel=%d cancelled=%v body=%s", cancel.Code, graph.cancelled, cancel.Body.String())
	}
}
