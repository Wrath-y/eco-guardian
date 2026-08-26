package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/zouyi/eco-guardian/internal/app"
	"github.com/zouyi/eco-guardian/internal/domain"
	graphclient "github.com/zouyi/eco-guardian/internal/graph/client"
	"github.com/zouyi/eco-guardian/internal/graph/impact"
	"github.com/zouyi/eco-guardian/internal/graph/impact/analysis"
	impactorchestration "github.com/zouyi/eco-guardian/internal/graph/impact/orchestration"
	"github.com/zouyi/eco-guardian/internal/graph/impact/planner"
	"github.com/zouyi/eco-guardian/internal/graph/impact/retrieval"
	"github.com/zouyi/eco-guardian/internal/graph/projector"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	"github.com/zouyi/eco-guardian/internal/validation"
)

const maxImpactCommandBytes = 1 << 20

type ImpactAnalysisServiceProvider func() app.ImpactAnalysisService

type ImpactProvider interface {
	impact.GraphReadinessReader
	impact.GraphQueryProvider
}

type ImpactServiceDependencies struct {
	Provider  ImpactProvider
	Explainer impact.EvidenceExplainer
	Submit    func(context.Context, domain.ID) error
	Clock     impact.Clock
	IDs       impact.IDGenerator
}

// ImpactAnalysisServiceFromProjectManager is the sole project/store
// composition seam for impact HTTP. Provider capability remains explicit: an
// unavailable local-rag process cannot be replaced by another Snapshot.
func ImpactAnalysisServiceFromProjectManager(manager *project.Manager, dependencies ImpactServiceDependencies) ImpactAnalysisServiceProvider {
	return func() app.ImpactAnalysisService {
		if manager == nil || dependencies.Provider == nil {
			return nil
		}
		handle, ok := manager.ActiveHandle()
		if !ok {
			return nil
		}
		provider, ok := handle.(interface{ Store() *store.Store })
		if !ok || provider.Store() == nil {
			return nil
		}
		s := provider.Store()
		clock := dependencies.Clock
		if clock == nil {
			clock = impactHTTPClock{}
		}
		ids := dependencies.IDs
		if ids == nil {
			ids = impactHTTPIDs{}
		}
		admission := &planner.Admission{Revisions: s, Validation: validation.NewValidationGate(s), Summaries: s, Graph: dependencies.Provider}
		submit := dependencies.Submit
		if submit == nil {
			worker := impactorchestration.Worker{Admission: admission, Revisions: s, Diffs: s, Provider: dependencies.Provider, Reports: s, Jobs: s, Events: s, IDs: ids, Clock: clock, Projector: projector.Descriptor{SchemaVersion: projector.ProjectionSchemaV1, Version: projector.ProjectorV1, Relations: projector.V1Relations(), Formatter: projector.V1Formatter{}}, PathOptions: analysis.PathOptions{Concurrency: 4}}
			submit = func(ctx context.Context, jobID domain.ID) error {
				workerContext := context.WithoutCancel(ctx)
				rootRequestID := uuid.NewString()
				go func() { _, _ = worker.Run(workerContext, jobID, rootRequestID) }()
				return nil
			}
		}
		return app.ImpactAnalysisApplication{Admission: admission, Provider: dependencies.Provider, Reports: s, Jobs: s, Events: s, IDs: ids, Clock: clock, Explainer: dependencies.Explainer, Submit: submit}
	}
}

type ImpactHandler struct{ service ImpactAnalysisServiceProvider }

func NewImpactHandler(service ImpactAnalysisServiceProvider) *ImpactHandler {
	return &ImpactHandler{service: service}
}

func (h *ImpactHandler) Register(engine *gin.Engine) {
	engine.POST("/api/v1/impact-analyses", h.create)
	engine.GET("/api/v1/impact-analyses/:id", h.get)
	engine.POST("/api/v1/impact-analyses/:id/path-expansions", h.expand)
	engine.POST("/api/v1/impact-analyses/:id/explanations", h.explain)
}

func (h *ImpactHandler) current(c *gin.Context) app.ImpactAnalysisService {
	if h.service == nil {
		writeImpactError(c, http.StatusServiceUnavailable, "INTERNAL_ERROR", "Impact analysis is unavailable", true, "")
		return nil
	}
	service := h.service()
	if service == nil {
		writeImpactError(c, http.StatusServiceUnavailable, "INTERNAL_ERROR", "Impact analysis is unavailable", true, "")
	}
	return service
}

func (h *ImpactHandler) create(c *gin.Context) {
	service := h.current(c)
	if service == nil {
		return
	}
	key, ok := impactIdempotencyKey(c)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxImpactCommandBytes+1))
	if err != nil || len(raw) > maxImpactCommandBytes {
		writeImpactError(c, http.StatusBadRequest, "INVALID_IMPACT_INPUT", "Impact request is invalid", false, "")
		return
	}
	command, err := planner.DecodeCommand(raw)
	if err != nil {
		writeImpactServiceError(c, err)
		return
	}
	// Keep the generated request model on the transport boundary as a drift
	// assertion while the normalized domain command remains transport-neutral.
	var generated riskdto.CreateImpactAnalysisRequest
	if err = json.Unmarshal(raw, &generated); err != nil {
		writeImpactError(c, http.StatusBadRequest, "INVALID_IMPACT_INPUT", "Impact request is invalid", false, "")
		return
	}
	job, replay, err := service.CreateImpactAnalysis(c.Request.Context(), command, key, requestID(c))
	if err != nil {
		writeImpactServiceError(c, err)
		return
	}
	location := "/api/v1/jobs/" + string(job.ID)
	resultURL := ""
	var resultID *riskdto.UUIDv7
	cacheHit := false
	if job.Result != nil {
		resultURL = job.Result.URL
		resultID = riskUUIDPtr(job.Result.ID)
		cacheHit = job.Status == sharedjob.Succeeded
	}
	c.Header("Location", location)
	c.JSON(http.StatusAccepted, riskdto.ImpactJobAccepted{Job: riskJobDTO(job, 0), Location: location, ResultType: riskdto.ImpactJobAcceptedResultType("impact_analysis"), ResultId: resultID, ResultUrl: resultURL, CacheHit: boolPtr(cacheHit || replay && job.Status == sharedjob.Succeeded)})
}

func (h *ImpactHandler) get(c *gin.Context) {
	service := h.current(c)
	if service == nil {
		return
	}
	id, ok := idParam(c, "IMPACT_REPORT_NOT_FOUND")
	if !ok {
		return
	}
	read, err := service.GetImpactAnalysis(c.Request.Context(), id, requestID(c))
	if err != nil {
		writeImpactServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, impactReportDTO(read))
}

func (h *ImpactHandler) expand(c *gin.Context) {
	service := h.current(c)
	if service == nil {
		return
	}
	if _, ok := impactIdempotencyKey(c); !ok {
		return
	}
	id, ok := idParam(c, "IMPACT_REPORT_NOT_FOUND")
	if !ok {
		return
	}
	var request riskdto.ImpactPathExpansionRequest
	if err := strictImpactJSON(c, &request); err != nil || strings.TrimSpace(request.TargetNodeId) == "" {
		writeImpactError(c, http.StatusBadRequest, "INVALID_IMPACT_INPUT", "Path expansion request is invalid", false, "")
		return
	}
	maxPaths := 0
	if request.MaxPaths != nil {
		maxPaths = *request.MaxPaths
	}
	result, err := service.ExpandImpactPaths(c.Request.Context(), id, request.TargetNodeId, maxPaths, requestID(c))
	if err != nil {
		writeImpactServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, impactExpansionDTO(result))
}

func (h *ImpactHandler) explain(c *gin.Context) {
	service := h.current(c)
	if service == nil {
		return
	}
	if _, ok := impactIdempotencyKey(c); !ok {
		return
	}
	id, ok := idParam(c, "IMPACT_REPORT_NOT_FOUND")
	if !ok {
		return
	}
	var request riskdto.ImpactExplanationRequest
	if err := strictImpactJSON(c, &request); err != nil || len(request.EvidenceRefs) == 0 || len(request.EvidenceRefs) > retrieval.MaxExplanationRefs {
		writeImpactError(c, http.StatusBadRequest, "INVALID_IMPACT_INPUT", "Explanation request is invalid", false, "")
		return
	}
	attempt, err := service.ExplainImpactEvidence(c.Request.Context(), id, request.EvidenceRefs)
	if err != nil {
		writeImpactServiceError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, impactExplanationDTO(attempt))
}

func ImpactJobResolver(provider ImpactAnalysisServiceProvider) DurableResolver {
	return DurableResolverFunc{
		Get: func(ctx context.Context, id domain.ID) (map[string]any, bool, error) {
			service := currentImpactService(provider)
			if service == nil {
				return nil, false, nil
			}
			job, err := service.GetImpactJob(ctx, id)
			if err != nil {
				return nil, false, nil
			}
			events, err := service.ListImpactJobEvents(ctx, id, 0)
			if err != nil {
				return nil, false, err
			}
			latest := int64(0)
			if len(events) > 0 {
				latest = events[len(events)-1].Ordinal
			}
			return riskJobMap(job, latest), true, nil
		},
		Cancel: func(ctx context.Context, id domain.ID) (map[string]any, bool, bool, error) {
			service := currentImpactService(provider)
			if service == nil {
				return nil, false, false, nil
			}
			job, changed, err := service.CancelImpactJob(ctx, id)
			if err != nil {
				return nil, false, false, nil
			}
			return riskJobMap(job, 0), true, changed, nil
		},
		Events: func(ctx context.Context, id domain.ID, after int64) ([]DurableJobEvent, error) {
			service := currentImpactService(provider)
			if service == nil {
				return nil, nil
			}
			events, err := service.ListImpactJobEvents(ctx, id, after)
			if err != nil {
				return nil, err
			}
			result := make([]DurableJobEvent, 0, len(events))
			for _, event := range events {
				result = append(result, DurableJobEvent{Ordinal: event.Ordinal, Payload: riskJobEventJSON(event)})
			}
			return result, nil
		},
	}
}

func currentImpactService(provider ImpactAnalysisServiceProvider) app.ImpactAnalysisService {
	if provider == nil {
		return nil
	}
	return provider()
}

func strictImpactJSON(c *gin.Context, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, maxImpactCommandBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func impactIdempotencyKey(c *gin.Context) (string, bool) {
	key := c.GetHeader("Idempotency-Key")
	if key == "" || key != strings.TrimSpace(key) || len(key) > 256 {
		writeImpactError(c, http.StatusBadRequest, "INVALID_IMPACT_INPUT", "A valid Idempotency-Key is required", false, "")
		return "", false
	}
	return key, true
}

func writeImpactServiceError(c *gin.Context, err error) {
	var provider *graphsync.ProviderError
	if errors.As(err, &provider) {
		code := provider.Code
		status, retryable := http.StatusServiceUnavailable, provider.Retryable
		switch graphclient.ClassifyError(provider) {
		case graphclient.ErrorNodeMissing:
			status, retryable = http.StatusConflict, false
		case graphclient.ErrorCapability, graphclient.ErrorQueryInvalid:
			status, retryable = http.StatusUnprocessableEntity, false
		case graphclient.ErrorNotReady:
			status = http.StatusConflict
		case graphclient.ErrorIntegrity, graphclient.ErrorHashMismatch, graphclient.ErrorHashConflict, graphclient.ErrorUnexpected:
			code, status, retryable = "PROVIDER_CONTRACT_MISMATCH", http.StatusConflict, false
		}
		writeImpactError(c, status, code, "Impact provider request failed", retryable, provider.RequestID)
		return
	}
	switch {
	case errors.Is(err, planner.ErrInvalidRevisionPair):
		writeImpactError(c, http.StatusConflict, "INVALID_REVISION_PAIR", "Revision pair is invalid", false, "")
	case errors.Is(err, planner.ErrNoBaseline):
		writeImpactError(c, http.StatusConflict, "NO_BASELINE", "No active release baseline exists", false, "")
	case errors.Is(err, planner.ErrGraphNotReady):
		writeImpactError(c, http.StatusConflict, "GRAPH_NOT_READY", "Graph snapshot is not query-ready", false, "")
	case errors.Is(err, planner.ErrGraphIdentity):
		writeImpactError(c, http.StatusConflict, "GRAPH_IDENTITY_MISMATCH", "Graph identity does not match the revision", false, "")
	case errors.Is(err, planner.ErrValidationRequired):
		writeImpactError(c, http.StatusConflict, "INVALID_REVISION_PAIR", "Full revision validation is required", false, "")
	case errors.Is(err, planner.ErrLimitExceeded):
		writeImpactError(c, http.StatusUnprocessableEntity, "LIMIT_EXCEEDED", "Impact limits exceed the supported bounds", false, "")
	case errors.Is(err, planner.ErrInvalidInput), errors.Is(err, retrieval.ErrInvalidExplanation):
		writeImpactError(c, http.StatusBadRequest, "INVALID_IMPACT_INPUT", "Impact request is invalid", false, "")
	case errors.Is(err, analysis.ErrProviderContract):
		writeImpactError(c, http.StatusConflict, "PROVIDER_CONTRACT_MISMATCH", "Provider evidence did not match the request", false, "")
	case errors.Is(err, store.ErrJobIdempotencyConflict):
		writeImpactError(c, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key conflicts with another request", false, "")
	case errors.Is(err, store.ErrImpactNotFound), errors.Is(err, store.ErrJobNotFound):
		writeImpactError(c, http.StatusNotFound, "IMPACT_REPORT_NOT_FOUND", "Impact report was not found", false, "")
	default:
		writeImpactError(c, http.StatusInternalServerError, "INTERNAL_ERROR", "Impact analysis is unavailable", true, "")
	}
}

func writeImpactError(c *gin.Context, status int, code, title string, retryable bool, providerRequestID string) {
	details := any(nil)
	if providerRequestID != "" {
		details = map[string]any{"provider_request_id": providerRequestID}
	}
	writeProblem(c, Problem{Type: "urn:eco:problem:" + strings.ToLower(strings.ReplaceAll(code, "_", "-")), Title: title, Status: status, Code: code, Retryable: retryable, RequestID: requestID(c), Details: details})
}

func impactReportDTO(read app.ImpactReportRead) riskdto.ImpactAnalysisReport {
	report, input := read.Report, read.Report.Input
	value := riskdto.ImpactAnalysisReport{Id: impactUUID(report.ID), InputHash: report.InputHash, ResultHash: report.ResultHash, AnalysisContractVersion: riskdto.ImpactAnalysisReportAnalysisContractVersion(input.AnalysisContractVersion), ProjectUuid: impactUUID(input.ProjectID), Base: impactRevisionDTO(input.Base), Target: impactRevisionDTO(input.Target), Filters: impactFiltersDTO(input.Filters), Limits: impactLimitsDTO(input.Limits), SuspectedOptions: impactSuspectedOptionsDTO(input.Suspected), Mode: riskdto.ImpactAnalysisReportMode(report.Mode), ChangedEntities: make([]riskdto.ImpactChangedEntity, 0, len(report.Changed)), DeterministicAffected: make([]riskdto.ImpactAffectedEntity, 0, len(report.Affected)), SuspectedAssociations: make([]riskdto.ImpactSuspectedEvidence, 0, len(report.Suspected)), SuspectedState: riskdto.ImpactSuspectedState(report.SuspectedState), Truncated: report.Truncated, TruncationReasons: impactReasonsDTO(report.Reasons), Warnings: nonnilStrings(report.Warnings), CacheHit: read.CacheHit, Freshness: riskdto.ImpactFreshness{Fresh: read.Freshness.Fresh, Reasons: nonnilStrings(read.Freshness.Reasons)}, CreatedAt: report.CreatedAt}
	for _, changed := range report.Changed {
		item := riskdto.ImpactChangedEntity{EntityId: impactUUID(changed.EntityID), Kind: riskdto.EntityKind(changed.Kind), ChangeKind: riskdto.ImpactChangedEntityChangeKind(changed.ChangeKind), FieldPaths: nonnilStrings(changed.FieldPaths), QueryEligible: changed.QueryEligible}
		if changed.TargetNodeID != "" {
			item.TargetNodeId = &changed.TargetNodeID
		}
		if changed.Ineligibility != "" {
			ineligibility := riskdto.ImpactChangedEntityIneligibility(changed.Ineligibility)
			item.Ineligibility = &ineligibility
		}
		value.ChangedEntities = append(value.ChangedEntities, item)
	}
	for _, affected := range report.Affected {
		value.DeterministicAffected = append(value.DeterministicAffected, impactAffectedDTO(affected))
	}
	for _, suspected := range report.Suspected {
		value.SuspectedAssociations = append(value.SuspectedAssociations, impactSuspectedDTO(suspected))
	}
	base := "/api/v1/impact-analyses/" + string(report.ID)
	value.Links.Self, value.Links.PathExpansions, value.Links.Explanations = base, base+"/path-expansions", base+"/explanations"
	if read.JobID.Valid() {
		value.Links.Job = "/api/v1/jobs/" + string(read.JobID)
	}
	return value
}

func impactRevisionDTO(value impact.RevisionIdentity) riskdto.ImpactRevisionIdentity {
	return riskdto.ImpactRevisionIdentity{RevisionId: impactUUID(value.RevisionID), ConfigHash: value.ConfigHash, VersionManifestHash: value.VersionManifestHash, GraphManifestHash: value.GraphManifestHash, GraphNodeCount: value.GraphNodeCount, GraphEdgeCount: value.GraphEdgeCount}
}

func impactFiltersDTO(value impact.Filters) riskdto.ImpactFilters {
	direction := riskdto.ImpactDirection(value.Direction)
	nodes := make([]riskdto.EntityKind, 0, len(value.NodeTypes))
	for _, item := range value.NodeTypes {
		nodes = append(nodes, riskdto.EntityKind(item))
	}
	edges := make([]riskdto.ImpactFiltersEdgeTypes, 0, len(value.EdgeTypes))
	for _, item := range value.EdgeTypes {
		edges = append(edges, riskdto.ImpactFiltersEdgeTypes(item))
	}
	relations := make([]interface{}, 0, len(value.RelationshipKinds))
	for _, item := range value.RelationshipKinds {
		relations = append(relations, item)
	}
	return riskdto.ImpactFilters{Direction: &direction, NodeTypes: &nodes, EdgeTypes: &edges, RelationshipKinds: &relations}
}

func impactLimitsDTO(value impact.Limits) riskdto.ImpactLimits {
	defaultPaths := riskdto.ImpactLimitsDefaultPathsPerTarget(value.DefaultPathsPerTarget)
	return riskdto.ImpactLimits{MaxDepth: intPtr(value.MaxDepth), MaxNodes: intPtr(value.MaxNodes), DefaultPathsPerTarget: &defaultPaths, ExpandedMaxPaths: intPtr(value.ExpandedMaxPaths)}
}

func impactSuspectedOptionsDTO(value impact.SuspectedOptions) riskdto.ImpactSuspectedOptions {
	return riskdto.ImpactSuspectedOptions{Enabled: boolPtr(value.Enabled), MaxSeeds: intPtr(value.MaxSeeds), MaxResults: intPtr(value.MaxResults), GraphMaxDepth: intPtr(value.GraphMaxDepth)}
}

func impactAffectedDTO(value impact.AffectedEntity) riskdto.ImpactAffectedEntity {
	evidenceRef, _ := impact.EvidenceID("node", value.Node)
	item := riskdto.ImpactAffectedEntity{Node: impactNodeDTO(value.Node), MinimumDepth: value.MinimumDepth, Direct: value.Direct, Indirect: value.Indirect, TagRule: value.TagRule, EvidenceRef: evidenceRef}
	if value.DefaultPath != nil {
		path := impactPathDTO(*value.DefaultPath)
		item.DefaultPath = &path
	}
	return item
}

func impactSuspectedDTO(value impact.SuspectedEvidence) riskdto.ImpactSuspectedEvidence {
	relations := make([]riskdto.ImpactSuspectedEvidenceRelationshipKinds, 0, len(value.RelationshipKinds))
	for _, relation := range value.RelationshipKinds {
		relations = append(relations, riskdto.ImpactSuspectedEvidenceRelationshipKinds(relation))
	}
	scoresRaw, _ := json.Marshal(value.Scores)
	scores := map[string]interface{}{}
	_ = json.Unmarshal(scoresRaw, &scores)
	evidenceRef, _ := impact.EvidenceID("suspected", value)
	item := riskdto.ImpactSuspectedEvidence{Rank: value.Rank, Node: impactNodeDTO(value.Node), CitationText: value.CitationText, SeedNodeId: value.SeedNodeID, RelationshipKinds: relations, Scores: scores, AlgorithmVersion: value.AlgorithmVersion, EvidenceRef: evidenceRef}
	if value.Path != nil {
		path := impactPathDTO(*value.Path)
		item.Path = &path
	}
	item.FtsGeneration, item.VectorGeneration, item.ModelProvider, item.Model = stringPtr(value.FTSGeneration), stringPtr(value.VectorGeneration), stringPtr(value.ModelProvider), stringPtr(value.Model)
	return item
}

func impactPathDTO(value impact.Path) riskdto.ImpactPath {
	nodes := make([]riskdto.ImpactGraphNode, 0, len(value.Nodes))
	for _, node := range value.Nodes {
		nodes = append(nodes, impactNodeDTO(node))
	}
	edges := make([]riskdto.ImpactGraphEdge, 0, len(value.Edges))
	for _, edge := range value.Edges {
		edges = append(edges, riskdto.ImpactGraphEdge{Id: edge.ID, From: edge.From, To: edge.To, Type: edge.Type, RelationKind: riskdto.ImpactGraphEdgeRelationKind(edge.RelationKind), Confidence: float32(edge.Confidence), Properties: nonnilMap(edge.Properties), Provenance: nonnilMap(edge.Provenance)})
	}
	return riskdto.ImpactPath{SourceNodeId: value.SourceNodeID, TargetNodeId: value.TargetNodeID, NodeIds: nonnilStrings(value.NodeIDs), EdgeIds: nonnilStrings(value.EdgeIDs), Nodes: nodes, Edges: edges, HopCount: value.HopCount, Truncated: value.Truncated, TruncationReasons: impactReasonsDTO(value.Reasons)}
}

func impactNodeDTO(value graphsync.Node) riskdto.ImpactGraphNode {
	return riskdto.ImpactGraphNode{Id: value.ID, Type: value.Type, Label: value.Label, Text: value.Text, Properties: nonnilMap(value.Properties), Provenance: nonnilMap(value.Provenance)}
}

func impactExpansionDTO(value app.ImpactPathExpansion) riskdto.ImpactPathExpansion {
	paths := make([]riskdto.ImpactPath, 0, len(value.Paths))
	for _, path := range value.Paths {
		paths = append(paths, impactPathDTO(path))
	}
	return riskdto.ImpactPathExpansion{ReportId: impactUUID(value.ReportID), ExpansionHash: value.ExpansionHash, TargetNodeId: value.TargetNodeID, Paths: paths, TruncationReasons: impactReasonsDTO(value.TruncationReasons), Warnings: nonnilStrings(value.Warnings)}
}

func impactExplanationDTO(value impact.ExplanationAttempt) riskdto.ImpactExplanation {
	result := riskdto.ImpactExplanation{Id: impactUUID(value.ID), ReportId: impactUUID(value.ReportID), Status: riskdto.ImpactExplanationStatus(value.Status), AiGenerated: true, EvidenceRefs: nonnilStrings(value.Refs), CreatedAt: value.CreatedAt}
	result.Text, result.Provider, result.Model, result.Diagnostic = stringPtr(value.Text), stringPtr(value.Provider), stringPtr(value.Model), stringPtr(value.Diagnostics)
	return result
}

func impactReasonsDTO(values []impact.TruncationReason) []riskdto.ImpactTruncationReason {
	result := make([]riskdto.ImpactTruncationReason, 0, len(values))
	for _, value := range values {
		result = append(result, riskdto.ImpactTruncationReason(value))
	}
	return result
}

func impactUUID(id domain.ID) riskdto.UUIDv7 {
	return uuid.MustParse(string(id))
}

func boolPtr(value bool) *bool { return &value }
func intPtr(value int) *int    { return &value }
func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func nonnilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
func nonnilMap(values map[string]any) map[string]interface{} {
	if values == nil {
		return map[string]interface{}{}
	}
	return values
}

type impactHTTPClock struct{}

func (impactHTTPClock) Now() time.Time { return time.Now().UTC() }

type impactHTTPIDs struct{}

func (impactHTTPIDs) New() (domain.ID, error) { return domain.NewID() }
