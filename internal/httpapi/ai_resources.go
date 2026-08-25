package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	aiapplication "github.com/zouyi/eco-guardian/internal/ai/application"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	aiorchestration "github.com/zouyi/eco-guardian/internal/ai/orchestration"
	"github.com/zouyi/eco-guardian/internal/ai/retrieval"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

const maxAIDesignJobBodyBytes int64 = 1 << 20

type AIDesignJobApplication interface {
	Submit(context.Context, aiapplication.DesignJobIntent, string) (aiorchestration.AdmissionResult, error)
}

type AIDraftPatchApplication interface {
	Read(context.Context, aicontract.PatchID) (aiapplication.DraftPatchReviewResource, error)
}

type AIJobRuntimeApplication interface {
	Get(context.Context, domain.ID) (aiorchestration.AIJobState, error)
	Cancel(context.Context, domain.ID) (aiorchestration.AIJobState, bool, error)
	Events(context.Context, domain.ID, int64) ([]aiorchestration.AIJobEvent, error)
}

type AIDesignJobServiceProvider func() AIDesignJobApplication
type AIDraftPatchServiceProvider func() AIDraftPatchApplication
type AIJobRuntimeServiceProvider func() AIJobRuntimeApplication

func AIDesignJobServiceFromProjectManager(manager *project.Manager) AIDesignJobServiceProvider {
	return func() AIDesignJobApplication {
		projectStore := aiStoreFromProjectManager(manager)
		if projectStore == nil {
			return nil
		}
		admission, err := aiorchestration.NewV1Admission(projectStore)
		if err != nil {
			return nil
		}
		return aiapplication.DesignJobService{ProjectID: projectStore.ProjectID(), Admission: admission, Jobs: projectStore}
	}
}

func AIDraftPatchServiceFromProjectManager(manager *project.Manager) AIDraftPatchServiceProvider {
	return func() AIDraftPatchApplication {
		projectStore := aiStoreFromProjectManager(manager)
		if projectStore == nil {
			return nil
		}
		return aiapplication.ReviewService{Repository: projectStore}
	}
}

func AIJobRuntimeServiceFromProjectManager(manager *project.Manager) AIJobRuntimeServiceProvider {
	return func() AIJobRuntimeApplication {
		projectStore := aiStoreFromProjectManager(manager)
		if projectStore == nil {
			return nil
		}
		return aiapplication.JobRuntimeService{Repository: projectStore}
	}
}

// AIJobResolver composes AI Jobs into the shared GET/cancel/SSE transport.
// Its event projection deliberately contains no Provider response body or
// retrieval ranking payload, especially when the Job has failed.
func AIJobResolver(provider AIJobRuntimeServiceProvider) DurableResolverFunc {
	get := func(ctx context.Context, id domain.ID) (aiorchestration.AIJobState, bool) {
		if provider == nil || !id.Valid() {
			return aiorchestration.AIJobState{}, false
		}
		service := provider()
		if service == nil {
			return aiorchestration.AIJobState{}, false
		}
		state, err := service.Get(ctx, id)
		return state, err == nil
	}
	return DurableResolverFunc{
		Get: func(ctx context.Context, id domain.ID) (map[string]any, bool, error) {
			state, found := get(ctx, id)
			if !found {
				return nil, false, nil
			}
			return aiJobStateJSON(state), true, nil
		},
		Cancel: func(ctx context.Context, id domain.ID) (map[string]any, bool, bool, error) {
			_, found := get(ctx, id)
			if !found {
				return nil, false, false, nil
			}
			state, _, err := provider().Cancel(ctx, id)
			if err != nil {
				return nil, true, false, err
			}
			return aiJobStateJSON(state), true, true, nil
		},
		Events: func(ctx context.Context, id domain.ID, after int64) ([]DurableJobEvent, error) {
			_, found := get(ctx, id)
			if !found {
				return nil, nil
			}
			events, err := provider().Events(ctx, id, after)
			if err != nil {
				return nil, err
			}
			result := make([]DurableJobEvent, len(events))
			for index, event := range events {
				result[index] = DurableJobEvent{Ordinal: event.Ordinal, Payload: aiJobEventJSON(event)}
			}
			return result, nil
		},
	}
}

func aiStoreFromProjectManager(manager *project.Manager) *store.Store {
	if manager == nil {
		return nil
	}
	handle, ok := manager.ActiveHandle()
	if !ok {
		return nil
	}
	provider, ok := handle.(interface{ Store() *store.Store })
	if !ok {
		return nil
	}
	return provider.Store()
}

type AIResourceHandler struct {
	jobs    AIDesignJobServiceProvider
	patches AIDraftPatchServiceProvider
}

func NewAIResourceHandler(jobs AIDesignJobServiceProvider, patches AIDraftPatchServiceProvider) *AIResourceHandler {
	return &AIResourceHandler{jobs: jobs, patches: patches}
}

func (handler *AIResourceHandler) Register(engine *gin.Engine) {
	engine.POST("/api/v1/ai-design-jobs", handler.createJob)
	engine.GET("/api/v1/draft-patches/:id", handler.getPatch)
}

func (handler *AIResourceHandler) createJob(c *gin.Context) {
	if !safeLoopbackOrigin(c.Request) {
		problem(c, http.StatusForbidden, "AI_INPUT_INVALID", "Request origin is not allowed")
		return
	}
	if handler.jobs == nil {
		problem(c, http.StatusNotFound, "PROJECT_NOT_OPEN", "No active project")
		return
	}
	service := handler.jobs()
	if service == nil {
		problem(c, http.StatusNotFound, "PROJECT_NOT_OPEN", "No active project")
		return
	}
	key, ok := aiIdempotencyKey(c)
	if !ok {
		return
	}
	var request riskdto.CreateAIDesignJobRequest
	if !decodeStrictAIJSONLimit(c, &request, maxAIDesignJobBodyBytes) {
		return
	}
	intent, err := aiDesignJobIntent(request)
	if err != nil {
		problem(c, http.StatusBadRequest, "AI_INPUT_INVALID", "Invalid AI design request")
		return
	}
	result, err := service.Submit(c.Request.Context(), intent, key)
	if err != nil {
		writeAIAdmissionError(c, err)
		return
	}
	location := "/api/v1/jobs/" + string(result.Job.ID)
	c.Header("Location", location)
	var patchURL *string
	if result.Job.Result != nil && result.Job.Result.Type == "draft_patch" {
		value := "/api/v1/draft-patches/" + string(result.Job.Result.ID)
		patchURL = &value
	}
	c.JSON(http.StatusAccepted, riskdto.AIDesignJobAccepted{Job: aiJobDTO(result.Job), Location: location, DraftPatchUrl: patchURL})
}

func (handler *AIResourceHandler) getPatch(c *gin.Context) {
	if handler.patches == nil {
		problem(c, http.StatusNotFound, "PROJECT_NOT_OPEN", "No active project")
		return
	}
	service := handler.patches()
	if service == nil {
		problem(c, http.StatusNotFound, "PROJECT_NOT_OPEN", "No active project")
		return
	}
	patchID, ok := aiPatchID(c)
	if !ok {
		return
	}
	resource, err := service.Read(c.Request.Context(), patchID)
	if err != nil {
		if errors.Is(err, aiapplication.ErrReviewNotFound) {
			problem(c, http.StatusNotFound, "AI_PATCH_NOT_FOUND", "DraftPatch was not found")
		} else if errors.Is(err, aiapplication.ErrReviewInvalid) {
			problem(c, http.StatusUnprocessableEntity, "AI_OUTPUT_INVALID", "DraftPatch resource is invalid")
		} else {
			problem(c, http.StatusInternalServerError, "STORAGE_FAILURE", "DraftPatch could not be read")
		}
		return
	}
	c.JSON(http.StatusOK, aiDraftPatchDTO(resource))
}

func aiDesignJobIntent(request riskdto.CreateAIDesignJobRequest) (aiapplication.DesignJobIntent, error) {
	intent := aiapplication.DesignJobIntent{BaseRevisionID: aicontract.RevisionID(request.BaseRevisionId.String()), Scenes: append([]string(nil), request.Scenes...)}
	intent.Goals = make([]aicontract.Goal, len(request.Goals))
	for index, value := range request.Goals {
		intent.Goals[index] = aicontract.Goal{ID: value.Id, Description: value.Description}
	}
	intent.Metrics = make([]aicontract.MetricGoal, len(request.Metrics))
	for index, value := range request.Metrics {
		target := ""
		if value.Target != nil {
			target = *value.Target
		}
		intent.Metrics[index] = aicontract.MetricGoal{MetricID: value.MetricId, Version: value.Version, Direction: aicontract.MetricDirection(value.Direction), Target: target, Unit: value.Unit}
	}
	intent.Constraints = make([]aicontract.Constraint, len(request.Constraints))
	for index, value := range request.Constraints {
		raw, err := json.Marshal(value.Value)
		if err != nil {
			return intent, err
		}
		canonical, err := aicontract.CanonicalToolPayload(raw)
		if err != nil {
			return intent, err
		}
		intent.Constraints[index] = aicontract.Constraint{ID: value.Id, Path: aicontract.FieldPath(value.Path), Operator: aicontract.ConstraintOperator(value.Operator), Value: canonical}
	}
	intent.AllowedTargets = make([]aicontract.AllowedTarget, len(request.AllowedTargets))
	for targetIndex, value := range request.AllowedTargets {
		target := aicontract.AllowedTarget{EntityID: aicontract.EntityID(value.EntityId.String()), Kind: string(value.Kind), ExpectedEntityVersion: int64(value.ExpectedEntityVersion), Paths: make([]aicontract.AllowedPath, len(value.Paths))}
		for pathIndex, path := range value.Paths {
			operations := make([]aicontract.PatchOperationKind, len(path.Operations))
			for operationIndex, operation := range path.Operations {
				operations[operationIndex] = aicontract.PatchOperationKind(operation)
			}
			target.Paths[pathIndex] = aicontract.AllowedPath{Path: aicontract.FieldPath(path.Path), Operations: operations}
		}
		intent.AllowedTargets[targetIndex] = target
	}
	if request.RequestedBudget != nil {
		value := request.RequestedBudget
		intent.RequestedBudget = &aicontract.Budget{Policy: aiVersionIdentity(value.Policy), BudgetLimits: aicontract.BudgetLimits{
			MaxFormatRepairs: int(value.MaxFormatRepairs), MaxProviderTurns: value.MaxProviderTurns, MaxToolCalls: value.MaxToolCalls,
			MaxSearchCandidates: value.MaxSearchCandidates, MaxDurationMillis: int64(value.MaxDurationMillis), MaxContextBytes: value.MaxContextBytes,
			MaxOutputBytes: value.MaxOutputBytes, MaxToolResultBytes: value.MaxToolResultBytes, RetrievalSeedLimit: value.RetrievalSeedLimit,
			RetrievalResultLimit: value.RetrievalResultLimit, RetrievalGraphDepth: value.RetrievalGraphDepth,
		}}
	}
	return intent, nil
}

func aiJobDTO(job sharedjob.Record) riskdto.Job {
	value := riskdto.Job{Id: uuid.MustParse(string(job.ID)), Kind: string(job.Kind), RevisionId: uuid.MustParse(string(job.RevisionID)), Status: riskdto.JobStatus(job.Status), RequestHash: job.RequestHash, EventsUrl: "/api/v1/jobs/" + string(job.ID) + "/events", PollAfterMs: 1000, CreatedAt: job.CreatedAt}
	updated := job.UpdatedAt
	value.UpdatedAt = &updated
	generation := int(job.CancelGeneration)
	value.CancelGeneration = &generation
	value.CancelRequestedAt = job.CancelRequestedAt
	if job.Result != nil {
		resultType := job.Result.Type
		resultID := uuid.MustParse(string(job.Result.ID))
		resultURL := job.Result.URL
		value.ResultType, value.ResultId, value.ResultUrl = &resultType, &resultID, &resultURL
	}
	return value
}

func aiJobStateJSON(state aiorchestration.AIJobState) map[string]any {
	value := map[string]any{
		"id": state.Job.ID, "kind": state.Job.Kind, "revision_id": state.Job.RevisionID, "status": state.Job.Status,
		"phase": state.Phase, "input_hash": state.Job.InputHash, "request_hash": state.Job.RequestHash,
		"events_url": "/api/v1/jobs/" + string(state.Job.ID) + "/events", "cancel_generation": state.Job.CancelGeneration,
		"cancel_requested_at": state.Job.CancelRequestedAt, "created_at": state.Job.CreatedAt, "updated_at": state.Job.UpdatedAt, "poll_after_ms": 1000,
	}
	if state.Job.Result != nil {
		value["result_type"], value["result_id"], value["result_url"] = state.Job.Result.Type, state.Job.Result.ID, state.Job.Result.URL
	}
	return value
}

func aiJobEventJSON(event aiorchestration.AIJobEvent) map[string]any {
	value := map[string]any{
		"job_id": event.JobID, "ordinal": event.Ordinal, "kind": event.Kind, "phase": event.Phase,
		"progress": event.Progress, "created_at": event.CreatedAt,
	}
	if event.AttemptID != "" {
		value["attempt_id"] = event.AttemptID
	}
	if event.WarningCode != "" {
		value["warning_code"] = event.WarningCode
	}
	if event.WarningRef != "" {
		value["warning_ref"] = event.WarningRef
	}
	if event.Tool != nil {
		value["tool"] = event.Tool
	}
	if event.RepairCount != 0 {
		value["repair_count"] = event.RepairCount
	}
	if event.Outcome != "" {
		value["outcome"] = event.Outcome
	}
	if event.SafeErrorCode != "" {
		value["error"] = map[string]any{"code": event.SafeErrorCode, "retryable": event.Retryable, "request_id": nullable(event.RequestID), "rebuild_required": event.RebuildRequired}
	}
	if event.Result != nil {
		value["result_type"], value["result_id"], value["result_url"] = event.Result.Type, event.Result.ID, event.Result.URL
	}
	return value
}

func aiDraftPatchDTO(resource aiapplication.DraftPatchReviewResource) riskdto.DraftPatchResource {
	value := riskdto.DraftPatchResource{
		Id: uuid.MustParse(string(resource.Patch.ID)), PatchHash: string(resource.Patch.Hash), Schema: aiVersionDTO(resource.Patch.Schema),
		BaseRevisionId: uuid.MustParse(string(resource.Patch.Base.ConfigRevisionID)), InputHash: string(resource.InputHash),
		EvidenceManifest: aiVersionDTO(resource.Patch.EvidenceManifestIdentity), Rationale: resource.Patch.Rationale,
		Assumptions: append([]string(nil), resource.Patch.Assumptions...), Attempts: make([]riskdto.AIAttemptProjection, len(resource.Attempts)),
		Freshness: riskdto.AIFreshnessProjection{State: riskdto.AIFreshnessProjectionState(resource.Freshness.State), ConflictingTargets: []uuid.UUID{}}, CreatedAt: resource.CreatedAt,
	}
	value.Targets = make([]riskdto.AIDraftTarget, len(resource.Patch.Targets))
	for targetIndex, target := range resource.Patch.Targets {
		operations := make([]riskdto.AIDraftOperation, len(target.Operations))
		for operationIndex, operation := range target.Operations {
			operations[operationIndex] = riskdto.AIDraftOperation{Ordinal: operation.Ordinal, Kind: riskdto.AIDraftOperationKind(operation.Kind), Path: string(operation.Path), Value: aiJSONValue(operation.Value), Evidence: aiEvidenceIDs(operation.Evidence)}
		}
		value.Targets[targetIndex] = riskdto.AIDraftTarget{EntityId: uuid.MustParse(string(target.EntityID)), Kind: riskdto.EntityKind(target.Kind), ExpectedEntityVersion: int(target.ExpectedEntityVersion), Operations: operations}
	}
	for index, attempt := range resource.Attempts {
		value.Attempts[index] = riskdto.AIAttemptProjection{Id: uuid.MustParse(string(attempt.ID)), Ordinal: attempt.Ordinal, Stage: riskdto.AIAttemptProjectionStage(attempt.Stage), Outcome: riskdto.AIAttemptProjectionOutcome(attempt.Outcome), Manifest: aiVersionDTO(attempt.Manifest), RepairRound: attempt.RepairRound}
		if attempt.ParentAttemptID.Valid() {
			parent := uuid.MustParse(string(attempt.ParentAttemptID))
			value.Attempts[index].ParentAttemptId = &parent
		}
	}
	for _, target := range resource.Freshness.ConflictingTarget {
		value.Freshness.ConflictingTargets = append(value.Freshness.ConflictingTargets, uuid.MustParse(string(target)))
	}
	if resource.Decision != nil {
		decision := aiDecisionDTO(*resource.Decision)
		value.Decision = &decision
	}
	if resource.Preview != nil {
		preview := aiPreviewDTO(*resource.Preview, resource.PreviewIssues, resource.RetrievalEvidence)
		value.Preview = &preview
	}
	self := "/api/v1/draft-patches/" + string(resource.Patch.ID)
	value.Links.Self, value.Links.Job, value.Links.Accept, value.Links.Discard = self, "/api/v1/jobs/"+string(resource.JobID), self+"/accept", self+"/discard"
	value.Links.FormalValidation = optionalString(resource.Formal.Validation)
	value.Links.FormalGraph = optionalString(resource.Formal.Graph)
	value.Links.FormalSimulation = optionalString(resource.Formal.Simulation)
	value.Links.FormalRisk = optionalString(resource.Formal.Risk)
	return value
}

func aiPreviewDTO(preview aicontract.Preview, issues []string, retrievalEvidence []retrieval.EvidenceRefV1) riskdto.AIPreviewProjection {
	details := make(map[aicontract.EvidenceID]retrieval.EvidenceRefV1, len(retrievalEvidence))
	for _, evidence := range retrievalEvidence {
		details[evidence.ID] = evidence
	}
	value := riskdto.AIPreviewProjection{Advisory: true, InputHash: string(preview.InputHash), ResultHash: string(preview.ResultHash), Acceptable: preview.Acceptable, Issues: append([]string(nil), issues...), Evaluators: make([]riskdto.AIVersionIdentity, len(preview.Evaluators)), Evidence: make([]riskdto.AIEvidenceRef, len(preview.Evidence))}
	for index, evaluator := range preview.Evaluators {
		value.Evaluators[index] = aiVersionDTO(evaluator)
	}
	for index, evidence := range preview.Evidence {
		projection := riskdto.AIEvidenceRef{Id: string(evidence.ID), Kind: riskdto.AIEvidenceRefKind(evidence.Kind), ManifestHash: string(evidence.ManifestHash), Generations: []riskdto.AIVersionIdentity{}, Scores: map[string]string{}, Warnings: []string{}}
		if detail, found := details[evidence.ID]; found {
			citation := detail.Citation
			mode := riskdto.AIEvidenceRefMode(detail.Mode)
			projection.Citation, projection.Mode, projection.Degraded = &citation, &mode, detail.Degraded
			projection.Scores = aiEvidenceScores(detail.Scores)
			for _, warning := range detail.Warnings {
				projection.Warnings = append(projection.Warnings, strings.TrimSpace(warning.Code+" "+warning.Message))
			}
			for _, generation := range detail.Generations {
				identity := aicontract.VersionIdentity{ID: generation.Component, Version: generation.Generation, Hash: aicontract.Hash(generation.ContentDigest)}
				if identity.Valid() && len(projection.Generations) < 3 {
					projection.Generations = append(projection.Generations, aiVersionDTO(identity))
				}
			}
		}
		value.Evidence[index] = projection
	}
	return value
}

func aiEvidenceScores(scores retrieval.PinnedScores) map[string]string {
	values := map[string]string{"rrf": scores.RRFScore, "graph": scores.GraphScore}
	if scores.BM25Rank != nil {
		values["bm25_rank"] = strconv.Itoa(*scores.BM25Rank)
	}
	if scores.BM25Score != nil {
		values["bm25"] = *scores.BM25Score
	}
	if scores.VectorRank != nil {
		values["vector_rank"] = strconv.Itoa(*scores.VectorRank)
	}
	if scores.VectorScore != nil {
		values["vector"] = *scores.VectorScore
	}
	if scores.RerankScore != nil {
		values["rerank"] = *scores.RerankScore
	}
	return values
}

func aiJSONValue(raw json.RawMessage) any {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil
	}
	return value
}

func aiEvidenceIDs(values []aicontract.EvidenceID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func aiVersionIdentity(value riskdto.AIVersionIdentity) aicontract.VersionIdentity {
	return aicontract.VersionIdentity{ID: value.Id, Version: value.Version, Hash: aicontract.Hash(value.Hash)}
}

func aiVersionDTO(value aicontract.VersionIdentity) riskdto.AIVersionIdentity {
	return riskdto.AIVersionIdentity{Id: value.ID, Version: value.Version, Hash: string(value.Hash)}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func writeAIAdmissionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, aiorchestration.ErrAdmissionInputInvalid), errors.Is(err, aiorchestration.ErrAdmissionTargetInvalid), errors.Is(err, aiorchestration.ErrAdmissionScopeInvalid), errors.Is(err, aiorchestration.ErrAdmissionCompatibility), errors.Is(err, aiapplication.ErrDesignJobInvalid):
		problem(c, http.StatusBadRequest, "AI_INPUT_INVALID", "Invalid AI design request")
	case errors.Is(err, aiorchestration.ErrAdmissionLimitExceeded):
		problem(c, http.StatusUnprocessableEntity, "AI_BUDGET_EXCEEDED", "AI design request exceeds a hard limit")
	case errors.Is(err, aiorchestration.ErrAdmissionSnapshotInvalid):
		problem(c, http.StatusConflict, "AI_SNAPSHOT_IDENTITY_MISMATCH", "AI base Snapshot identity does not match")
	case errors.Is(err, aiorchestration.ErrAdmissionUnavailable):
		problem(c, http.StatusServiceUnavailable, "AI_CAPABILITY_UNAVAILABLE", "AI design admission is unavailable")
	case errors.Is(err, store.ErrJobIdempotencyConflict):
		problem(c, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "AI Job idempotency request conflicts")
	default:
		problem(c, http.StatusInternalServerError, "STORAGE_FAILURE", "AI design Job could not be admitted")
	}
}
