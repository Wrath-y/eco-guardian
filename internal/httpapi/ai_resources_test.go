package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	aiapplication "github.com/zouyi/eco-guardian/internal/ai/application"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
	"github.com/zouyi/eco-guardian/internal/app"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type aiDesignJobApplicationFake struct {
	intent aiapplication.DesignJobIntent
	key    string
	result aiorchestration.AdmissionResult
	err    error
}

func (fake *aiDesignJobApplicationFake) Submit(_ context.Context, intent aiapplication.DesignJobIntent, key string) (aiorchestration.AdmissionResult, error) {
	fake.intent, fake.key = intent, key
	return fake.result, fake.err
}

type aiDraftPatchApplicationFake struct {
	patchID  aicontract.PatchID
	resource aiapplication.DraftPatchReviewResource
	err      error
}

type aiJobRuntimeApplicationFake struct {
	state  aiorchestration.AIJobState
	events []aiorchestration.AIJobEvent
}

func (fake *aiJobRuntimeApplicationFake) Get(_ context.Context, id domain.ID) (aiorchestration.AIJobState, error) {
	if fake.state.Job.ID != id {
		return aiorchestration.AIJobState{}, aiapplication.ErrJobRuntimeInvalid
	}
	return fake.state, nil
}

func (fake *aiJobRuntimeApplicationFake) Cancel(_ context.Context, id domain.ID) (aiorchestration.AIJobState, bool, error) {
	if fake.state.Job.ID != id {
		return aiorchestration.AIJobState{}, false, aiapplication.ErrJobRuntimeInvalid
	}
	return fake.state, true, nil
}

func (fake *aiJobRuntimeApplicationFake) Events(_ context.Context, id domain.ID, after int64) ([]aiorchestration.AIJobEvent, error) {
	if fake.state.Job.ID != id {
		return nil, aiapplication.ErrJobRuntimeInvalid
	}
	result := []aiorchestration.AIJobEvent{}
	for _, event := range fake.events {
		if event.Ordinal > after {
			result = append(result, event)
		}
	}
	return result, nil
}

func (fake *aiDraftPatchApplicationFake) Read(_ context.Context, patchID aicontract.PatchID) (aiapplication.DraftPatchReviewResource, error) {
	fake.patchID = patchID
	return fake.resource, fake.err
}

func aiResourceEngine(jobs AIDesignJobApplication, patches AIDraftPatchApplication) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewAIResourceHandler(func() AIDesignJobApplication { return jobs }, func() AIDraftPatchApplication { return patches }).Register(engine)
	return engine
}

func TestAIResourceHandlersCreateJobAndReadCompleteDraftPatch(t *testing.T) {
	now := time.Date(2026, 8, 25, 11, 0, 0, 0, time.UTC)
	jobID, baseID, targetID := aiHTTPID("421"), aiHTTPID("422"), aiHTTPID("423")
	job := sharedjob.Record{ID: jobID, ProjectID: aiHTTPID("424"), Kind: aiorchestration.AIDesignJobKind, RevisionID: baseID, InputHash: aiHTTPHash("a"), IdempotencyKey: "job-key", RequestHash: aiHTTPHash("a"), Status: sharedjob.Queued, CreatedAt: now, UpdatedAt: now}
	jobs := &aiDesignJobApplicationFake{result: aiorchestration.AdmissionResult{Job: job}}
	resource := aiHTTPReviewResource(t, job, targetID, now)
	patches := &aiDraftPatchApplicationFake{resource: resource}
	engine := aiResourceEngine(jobs, patches)

	body := `{"base_revision_id":"` + string(baseID) + `","goals":[{"id":"balance","description":"Tune the target."}],"metrics":[{"metric_id":"metric-dps","version":"v1","direction":"minimize","unit":"points_per_second"}],"constraints":[],"allowed_targets":[{"entity_id":"` + string(targetID) + `","kind":"tag","expected_entity_version":1,"paths":[{"path":"/payload/category","operations":["replace"]}]}],"scenes":["single-target-30s"]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/ai-design-jobs", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "job-key")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Location") != "/api/v1/jobs/"+string(jobID) || !strings.Contains(response.Body.String(), `"draft_patch_url":null`) {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if jobs.key != "job-key" || jobs.intent.BaseRevisionID != aicontract.RevisionID(baseID) || len(jobs.intent.AllowedTargets) != 1 || jobs.intent.AllowedTargets[0].EntityID != aicontract.EntityID(targetID) {
		t.Fatalf("intent=%#v key=%q", jobs.intent, jobs.key)
	}

	read := httptest.NewRequest(http.MethodGet, "/api/v1/draft-patches/"+string(resource.Patch.ID), nil)
	readResponse := httptest.NewRecorder()
	engine.ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusOK || patches.patchID != resource.Patch.ID {
		t.Fatalf("status=%d patch=%s body=%s", readResponse.Code, patches.patchID, readResponse.Body.String())
	}
	for _, fragment := range []string{`"patch_hash":"` + string(resource.Patch.Hash) + `"`, `"preview":{"acceptable":true`, `"evidence":["evidence-1"]`, `"formal_validation":null`, `"decision":null`} {
		if !strings.Contains(readResponse.Body.String(), fragment) {
			t.Fatalf("missing %s in %s", fragment, readResponse.Body.String())
		}
	}
}

func TestAIJobHandlerRejectsUnknownMembersAndBoundedPayloads(t *testing.T) {
	engine := aiResourceEngine(&aiDesignJobApplicationFake{}, &aiDraftPatchApplicationFake{})
	for name, body := range map[string]string{
		"unknown":   `{"base_revision_id":"` + string(aiHTTPID("431")) + `","goals":[],"metrics":[],"constraints":[],"allowed_targets":[],"scenes":[],"credential":"canary"}`,
		"oversized": strings.Repeat("x", int(maxAIDesignJobBodyBytes)+1),
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/ai-design-jobs", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "job-key")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"AI_INPUT_INVALID"`) || strings.Contains(response.Body.String(), "canary") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAIJobResolverProjectsSafeFailedStageThroughSharedRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	job := sharedjob.Record{
		ID: aiHTTPID("441"), ProjectID: aiHTTPID("442"), Kind: aiorchestration.AIDesignJobKind, RevisionID: aiHTTPID("443"),
		InputHash: aiHTTPHash("a"), IdempotencyKey: "job-key", RequestHash: aiHTTPHash("b"), Status: sharedjob.Failed, CreatedAt: now, UpdatedAt: now,
	}
	failure := aiorchestration.StageFailure{Code: "AI_PROVIDER_TIMEOUT", Retryable: true, RequestID: "provider-request_123"}
	draft, err := failure.TerminalEvent(job.ID, aicontract.AttemptID("attempt-1"), aiorchestration.PhaseProviderToolLoop, "terminal-1", aicontract.OutcomeFailed)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &aiJobRuntimeApplicationFake{state: aiorchestration.AIJobState{Job: job, Phase: aiorchestration.PhaseProviderToolLoop}, events: []aiorchestration.AIJobEvent{{AIJobEventDraft: draft, Ordinal: 1, CreatedAt: now}}}
	handler := NewVersionHandler(nil)
	handler.service = func() app.VersioningService { return &fakeVersionService{} }
	handler.resolvers = []DurableResolver{AIJobResolver(func() AIJobRuntimeApplication { return runtime })}
	engine := gin.New()
	handler.Register(engine)

	get := httptest.NewRecorder()
	engine.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+string(job.ID), nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"phase":"provider/tool_loop"`) {
		t.Fatalf("status=%d body=%s", get.Code, get.Body.String())
	}
	stream := httptest.NewRecorder()
	engine.ServeHTTP(stream, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+string(job.ID)+"/events", nil))
	body := stream.Body.String()
	for _, fragment := range []string{`"code":"AI_PROVIDER_TIMEOUT"`, `"retryable":true`, `"request_id":"provider-request_123"`, "event: terminal"} {
		if stream.Code != http.StatusOK || !strings.Contains(body, fragment) {
			t.Fatalf("missing %s status=%d body=%s", fragment, stream.Code, body)
		}
	}
	for _, forbidden := range []string{"provider-secret", "ranking", "structured_response", "partial"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("leaked %q in %s", forbidden, body)
		}
	}
}

func aiHTTPReviewResource(t *testing.T, job sharedjob.Record, targetID domain.ID, now time.Time) aiapplication.DraftPatchReviewResource {
	t.Helper()
	fixture := aicontract.V1Fixture()
	registries, err := aicontract.NewV1RegistrySet()
	if err != nil {
		t.Fatal(err)
	}
	budget, err := registries.ResolvedLimits()
	if err != nil {
		t.Fatal(err)
	}
	hash := aicontract.Hash(aiHTTPHash("a"))
	base := aicontract.FrozenBaseIdentity{ProjectID: aicontract.ProjectID(job.ProjectID), ConfigRevisionID: aicontract.RevisionID(job.RevisionID), ConfigHash: hash, VersionManifestHash: hash, MaterializationHash: hash, GraphNamespace: string(job.ProjectID), GraphSnapshot: string(job.RevisionID), GraphContentHash: hash}
	allowed := aicontract.AllowedTarget{EntityID: aicontract.EntityID(targetID), Kind: string(domain.KindTag), ExpectedEntityVersion: 1, Paths: []aicontract.AllowedPath{{Path: "/payload/category", Operations: []aicontract.PatchOperationKind{aicontract.OperationReplace}}}}
	input := aicontract.AIDesignInputV1{Schema: fixture.PatchSchema.Identity, Base: base, Baseline: aicontract.BaselineIdentity{Kind: aicontract.BaselineNone}, Goals: []aicontract.Goal{{ID: "balance", Description: "Tune the target."}}, Metrics: []aicontract.MetricGoal{{MetricID: "metric-dps", Version: "v1", Direction: aicontract.MetricMinimize, Unit: "points_per_second"}}, AllowedTargets: []aicontract.AllowedTarget{allowed}, Scenes: []string{"single-target-30s"}, Budget: budget, RequiredVersions: []aicontract.VersionIdentity{{ID: "validation", Version: "v1", Hash: hash}}}
	inputHash, err := aicontract.HashAIDesignInputV1(input)
	if err != nil {
		t.Fatal(err)
	}
	patch := aicontract.DraftPatchV1{ID: aicontract.PatchID(aiHTTPID("425")), Schema: fixture.PatchSchema.Identity, Base: base, EvidenceManifestIdentity: aicontract.VersionIdentity{ID: "retrieval-evidence", Version: "v1", Hash: hash}, Targets: []aicontract.DraftTarget{{EntityID: aicontract.EntityID(targetID), Kind: string(domain.KindTag), ExpectedEntityVersion: 1, Operations: []aicontract.DraftOperation{{Ordinal: 1, Kind: aicontract.OperationReplace, Path: "/payload/category", Value: json.RawMessage(`"mechanic"`), Evidence: []aicontract.EvidenceID{"evidence-1"}}}}}, Rationale: "Evidence-backed adjustment.", Assumptions: []string{"The scene remains representative."}, Hash: aicontract.Hash(aiHTTPHash("0"))}
	patch.Hash, err = aicontract.HashDraftPatch(patch)
	if err != nil {
		t.Fatal(err)
	}
	preview := aicontract.Preview{Advisory: true, InputHash: inputHash, ResultHash: aicontract.Hash(aiHTTPHash("b")), Evaluators: []aicontract.VersionIdentity{{ID: "simulation", Version: "v1", Hash: hash}}, Evidence: []aicontract.EvidenceRef{}, Acceptable: true}
	return aiapplication.DraftPatchReviewResource{Patch: patch, JobID: job.ID, Input: input, InputHash: inputHash, Attempts: []aiapplication.AttemptReadProjection{{ID: aicontract.AttemptID(aiHTTPID("426")), Ordinal: 1, Stage: aicontract.StagePatchSealed, Outcome: aicontract.OutcomeSucceeded, Manifest: aicontract.VersionIdentity{ID: "attempt-manifest", Version: "v1", Hash: hash}, RepairRound: 0}}, Preview: &preview, PreviewIssues: []string{}, RetrievalEvidence: []retrieval.EvidenceRefV1{}, Freshness: aicontract.Freshness{State: aicontract.FreshnessFresh}, CreatedAt: now}
}
