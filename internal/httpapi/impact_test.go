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
	"github.com/zouyi/eco-guardian/internal/app"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	impactreport "github.com/zouyi/eco-guardian/internal/graph/impact/report"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
)

type impactHTTPServiceFake struct {
	job       sharedjob.Record
	read      app.ImpactReportRead
	expansion app.ImpactPathExpansion
	attempt   impact.ExplanationAttempt
	err       error
	created   int
	canceled  int
}

func (f *impactHTTPServiceFake) CreateImpactAnalysis(context.Context, impact.Command, string, string) (sharedjob.Record, bool, error) {
	f.created++
	return f.job, false, f.err
}
func (f *impactHTTPServiceFake) GetImpactAnalysis(context.Context, domain.ID, string) (app.ImpactReportRead, error) {
	return f.read, f.err
}
func (f *impactHTTPServiceFake) ExpandImpactPaths(context.Context, domain.ID, string, int, string) (app.ImpactPathExpansion, error) {
	return f.expansion, f.err
}
func (f *impactHTTPServiceFake) ExplainImpactEvidence(context.Context, domain.ID, []string) (impact.ExplanationAttempt, error) {
	return f.attempt, f.err
}
func (f *impactHTTPServiceFake) GetImpactJob(context.Context, domain.ID) (sharedjob.Record, error) {
	if f.err != nil {
		return sharedjob.Record{}, f.err
	}
	return f.job, nil
}
func (f *impactHTTPServiceFake) CancelImpactJob(context.Context, domain.ID) (sharedjob.Record, bool, error) {
	f.canceled++
	return f.job, true, f.err
}
func (f *impactHTTPServiceFake) ListImpactJobEvents(context.Context, domain.ID, int64) ([]sharedjob.Event, error) {
	return []sharedjob.Event{{JobID: f.job.ID, Ordinal: 1, Phase: "ADMISSION", Progress: 5, CreatedAt: f.job.CreatedAt}}, f.err
}

func TestImpactHandlerCreatesSharedJobAndRejectsNoncanonicalCommands(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projectID, _ := domain.NewID()
	baseID, _ := domain.NewID()
	targetID, _ := domain.NewID()
	jobID, _ := domain.NewID()
	now := time.Now().UTC()
	hash := strings.Repeat("a", 64)
	service := &impactHTTPServiceFake{job: sharedjob.Record{ID: jobID, ProjectID: projectID, Kind: "impact_analysis", RevisionID: targetID, InputHash: hash, IdempotencyKey: "impact-key", RequestHash: hash, Status: sharedjob.Queued, CreatedAt: now, UpdatedAt: now}}
	engine := gin.New()
	NewImpactHandler(func() app.ImpactAnalysisService { return service }).Register(engine)
	body := `{"project_uuid":"` + string(projectID) + `","base_revision_id":"` + string(baseID) + `","target_revision_id":"` + string(targetID) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/impact-analyses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "impact-key")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Header().Get("Location") != "/api/v1/jobs/"+string(jobID) || !strings.Contains(response.Body.String(), `"result_type":"impact_analysis"`) {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/impact-analyses", strings.NewReader(strings.Replace(body, `"project_uuid":`, `"unknown":1,"project_uuid":`, 1)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "impact-key-2")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || service.created != 1 {
		t.Fatalf("status=%d created=%d body=%s", response.Code, service.created, response.Body.String())
	}
}

func TestImpactHandlerProjectsImmutableReportAndChildResources(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reportID, _ := domain.NewID()
	projectID, _ := domain.NewID()
	baseID, _ := domain.NewID()
	targetID, _ := domain.NewID()
	jobID, _ := domain.NewID()
	attemptID, _ := domain.NewID()
	hash := strings.Repeat("b", 64)
	now := time.Now().UTC()
	input := impact.Input{ProjectID: projectID, AnalysisContractVersion: impact.AnalysisContractVersion, Base: impact.RevisionIdentity{RevisionID: baseID, ConfigHash: hash, VersionManifestHash: hash, GraphManifestHash: hash}, Target: impact.RevisionIdentity{RevisionID: targetID, ConfigHash: hash, VersionManifestHash: hash, GraphManifestHash: hash}, Filters: impact.Filters{RelationshipKinds: []string{"explicit"}, Direction: impact.DirectionIncoming}, Limits: impact.Limits{MaxDepth: 3, MaxNodes: 500, DefaultPathsPerTarget: 1, ExpandedMaxPaths: 20}, Suspected: impact.SuspectedOptions{MaxSeeds: 20, MaxResults: 20, GraphMaxDepth: 2}}
	report := impact.Report{ID: reportID, Input: input, InputHash: hash, ResultHash: hash, Mode: impact.ReverseDependencyImpact, Changed: []impact.ChangedEntity{}, Affected: []impact.AffectedEntity{}, Suspected: []impact.SuspectedEvidence{}, SuspectedState: impact.SuspectedDisabled, Reasons: []impact.TruncationReason{}, Warnings: []string{}, CreatedAt: now}
	service := &impactHTTPServiceFake{read: app.ImpactReportRead{Report: report, Freshness: impactreport.Freshness{Fresh: true}, JobID: jobID}, expansion: app.ImpactPathExpansion{ReportID: reportID, ExpansionHash: hash, TargetNodeID: "node-1", Paths: []impact.Path{}, TruncationReasons: []impact.TruncationReason{}, Warnings: []string{}}, attempt: impact.ExplanationAttempt{ID: attemptID, ReportID: reportID, InputHash: hash, Status: "failed", Diagnostics: "AI_UNAVAILABLE", CreatedAt: now}}
	engine := gin.New()
	NewImpactHandler(func() app.ImpactAnalysisService { return service }).Register(engine)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/impact-analyses/"+string(reportID), nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"fresh":true`) || !strings.Contains(response.Body.String(), `"path_expansions"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/impact-analyses/"+string(reportID)+"/path-expansions", strings.NewReader(`{"target_node_id":"node-1"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "expand-1")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), hash) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestImpactProviderErrorsExposeOnlyStableCorrelation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &impactHTTPServiceFake{err: &graphsync.ProviderError{Code: "GRAPH_STORE_UNAVAILABLE", Message: "secret /tmp/database.sqlite", RequestID: "provider-request-7", Retryable: true, Details: map[string]any{"sql": "SELECT secret"}}}
	engine := gin.New()
	NewImpactHandler(func() app.ImpactAnalysisService { return service }).Register(engine)
	projectID, _ := domain.NewID()
	baseID, _ := domain.NewID()
	targetID, _ := domain.NewID()
	body := `{"project_uuid":"` + string(projectID) + `","base_revision_id":"` + string(baseID) + `","target_revision_id":"` + string(targetID) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/impact-analyses", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "impact-key")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "database.sqlite") || strings.Contains(response.Body.String(), "SELECT") || !strings.Contains(response.Body.String(), "provider-request-7") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestImpactJobResolverUsesUnifiedCancelAndOrdinalEvents(t *testing.T) {
	jobID, _ := domain.NewID()
	projectID, _ := domain.NewID()
	revisionID, _ := domain.NewID()
	hash := strings.Repeat("c", 64)
	now := time.Now().UTC()
	service := &impactHTTPServiceFake{job: sharedjob.Record{ID: jobID, ProjectID: projectID, Kind: "impact_analysis", RevisionID: revisionID, InputHash: hash, IdempotencyKey: "impact-key", RequestHash: hash, Status: sharedjob.Running, CreatedAt: now, UpdatedAt: now}}
	resolver := ImpactJobResolver(func() app.ImpactAnalysisService { return service })
	body, found, err := resolver.GetJob(context.Background(), jobID)
	if err != nil || !found || body["kind"] != "impact_analysis" {
		t.Fatalf("found=%v err=%v body=%v", found, err, body)
	}
	_, found, changed, err := resolver.CancelJob(context.Background(), jobID)
	if err != nil || !found || !changed || service.canceled != 1 {
		t.Fatalf("found=%v changed=%v err=%v canceled=%d", found, changed, err, service.canceled)
	}
	events, err := resolver.ListJobEvents(context.Background(), jobID, 0)
	if err != nil || len(events) != 1 || events[0].Ordinal != 1 {
		t.Fatalf("events=%v err=%v", events, err)
	}
	if _, err = json.Marshal(events[0].Payload); err != nil {
		t.Fatal(err)
	}
}

var _ app.ImpactAnalysisService = (*impactHTTPServiceFake)(nil)
