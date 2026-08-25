package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/risk/orchestration"
	"github.com/zouyi/eco-guardian/internal/risk/threshold"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

const maxRiskReviewResponseBytes = 8 << 20

// ReviewService is the generated-DTO application boundary. Implementations
// own project-scoped materialization, threshold selection, Gate evaluation and
// persistence; the HTTP adapter never calculates or queries storage directly.
type ReviewService interface {
	CreateRiskReview(context.Context, riskdto.RiskReviewCommand, string) (sharedjob.Record, error)
	GetRiskReview(context.Context, domain.ID) (riskdto.RiskReview, error)
	GetRiskJob(context.Context, domain.ID) (sharedjob.Record, error)
	CancelRiskJob(context.Context, domain.ID) (sharedjob.Record, bool, error)
	ListRiskJobEvents(context.Context, domain.ID, int64) ([]sharedjob.Event, error)
}

type ReviewServiceProvider func() ReviewService

type ReviewHandler struct{ service ReviewServiceProvider }

func NewRiskReviewHandler(service ReviewServiceProvider) *ReviewHandler {
	return &ReviewHandler{service: service}
}

func (h *ReviewHandler) Register(engine *gin.Engine) {
	engine.POST("/api/v1/risk-reviews", h.create)
	engine.GET("/api/v1/risk-reviews/:id", h.get)
}

func (h *ReviewHandler) current(c *gin.Context) ReviewService {
	if h.service == nil {
		problem(c, http.StatusNotFound, "PROJECT_NOT_OPEN", "No active project")
		return nil
	}
	service := h.service()
	if service == nil {
		problem(c, http.StatusNotFound, "PROJECT_NOT_OPEN", "No active project")
	}
	return service
}

func (h *ReviewHandler) create(c *gin.Context) {
	service := h.current(c)
	if service == nil {
		return
	}
	key := c.GetHeader("Idempotency-Key")
	if key == "" || key != strings.TrimSpace(key) || len(key) > 256 {
		writeRiskError(c, NewRiskAPIError(http.StatusBadRequest, "RISK_IDEMPOTENCY_REQUIRED", "A valid Idempotency-Key is required", false))
		return
	}
	command, err := DecodeRiskReviewCommand(c.Request.Body)
	if err != nil {
		writeRiskError(c, NewRiskAPIError(http.StatusBadRequest, "RISK_COMMAND_INVALID", "Risk review command is invalid", false))
		return
	}
	job, err := service.CreateRiskReview(c.Request.Context(), command, key)
	if err != nil {
		writeRiskServiceError(c, err)
		return
	}
	location := "/api/v1/jobs/" + string(job.ID)
	c.Header("Location", location)
	c.JSON(http.StatusAccepted, riskdto.RiskJobAccepted{Job: riskJobDTO(job, 0), Location: location})
}

func (h *ReviewHandler) get(c *gin.Context) {
	service := h.current(c)
	if service == nil {
		return
	}
	id, ok := idParam(c, "RISK_REVIEW_NOT_FOUND")
	if !ok {
		return
	}
	review, err := service.GetRiskReview(c.Request.Context(), id)
	if err != nil {
		writeRiskServiceError(c, err)
		return
	}
	body, err := json.Marshal(review)
	if err != nil || len(body) > maxRiskReviewResponseBytes {
		writeRiskError(c, NewRiskAPIError(http.StatusInternalServerError, "STORAGE_FAILURE", "Risk review result is unavailable", true))
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

func RiskJobResolver(provider ReviewServiceProvider) DurableResolver {
	return DurableResolverFunc{
		Get: func(ctx context.Context, id domain.ID) (map[string]any, bool, error) {
			if provider == nil {
				return nil, false, nil
			}
			service := provider()
			if service == nil {
				return nil, false, nil
			}
			job, err := service.GetRiskJob(ctx, id)
			if err != nil {
				if isRiskNotFound(err) {
					return nil, false, nil
				}
				return nil, false, err
			}
			events, err := service.ListRiskJobEvents(ctx, id, 0)
			if err != nil {
				return nil, false, err
			}
			latest := int64(0)
			if len(events) != 0 {
				latest = events[len(events)-1].Ordinal
			}
			return riskJobMap(job, latest), true, nil
		},
		Cancel: func(ctx context.Context, id domain.ID) (map[string]any, bool, bool, error) {
			if provider == nil {
				return nil, false, false, nil
			}
			service := provider()
			if service == nil {
				return nil, false, false, nil
			}
			job, changed, err := service.CancelRiskJob(ctx, id)
			if err != nil {
				if isRiskNotFound(err) {
					return nil, false, false, nil
				}
				return nil, true, false, err
			}
			return riskJobMap(job, 0), true, changed, nil
		},
		Events: func(ctx context.Context, id domain.ID, after int64) ([]DurableJobEvent, error) {
			if provider == nil {
				return nil, nil
			}
			service := provider()
			if service == nil {
				return nil, nil
			}
			events, err := service.ListRiskJobEvents(ctx, id, after)
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

func riskJobDTO(job sharedjob.Record, latest int64) riskdto.Job {
	cancelGeneration := int(job.CancelGeneration)
	value := riskdto.Job{Id: riskUUID(job.ID), Kind: string(job.Kind), RevisionId: riskUUID(job.RevisionID), Status: riskdto.JobStatus(job.Status), RequestHash: job.RequestHash, EventsUrl: "/api/v1/jobs/" + string(job.ID) + "/events", PollAfterMs: 1000, CancelGeneration: &cancelGeneration, CancelRequestedAt: job.CancelRequestedAt, CreatedAt: job.CreatedAt, UpdatedAt: &job.UpdatedAt}
	if latest > 0 {
		ordinal := int(latest)
		value.LatestEventOrdinal = &ordinal
	}
	if job.Result != nil {
		resultType, resultURL := job.Result.Type, job.Result.URL
		value.ResultType, value.ResultId, value.ResultUrl = &resultType, riskUUIDPtr(job.Result.ID), &resultURL
	}
	return value
}

func riskJobMap(job sharedjob.Record, latest int64) map[string]any {
	body, _ := json.Marshal(riskJobDTO(job, latest))
	var result map[string]any
	_ = json.Unmarshal(body, &result)
	return result
}

func riskJobEventJSON(event sharedjob.Event) map[string]any {
	value := map[string]any{"job_id": event.JobID, "ordinal": event.Ordinal, "phase": event.Phase, "progress": event.Progress, "created_at": event.CreatedAt}
	if event.Warning != "" {
		value["warning"] = event.Warning
	}
	if event.SafeError != "" {
		value["error"] = event.SafeError
	}
	if event.Result != nil {
		value["result_type"], value["result_id"], value["result_url"] = event.Result.Type, event.Result.ID, event.Result.URL
	}
	return value
}

func riskUUID(id domain.ID) riskdto.UUIDv7 {
	value, _ := uuid.Parse(string(id))
	return value
}

func riskUUIDPtr(id domain.ID) *riskdto.UUIDv7 {
	value := riskUUID(id)
	return &value
}

// RiskAPIError freezes a public Problem Details classification while keeping
// underlying SQL, paths, stack data and arbitrary payloads out of responses.
type RiskAPIError struct {
	Status    int
	Code      string
	Title     string
	Retryable bool
}

func NewRiskAPIError(status int, code, title string, retryable bool) RiskAPIError {
	return RiskAPIError{Status: status, Code: code, Title: title, Retryable: retryable}
}

func (e RiskAPIError) Error() string { return e.Code }

func writeRiskError(c *gin.Context, apiError RiskAPIError) {
	canonical, found := riskProblemCatalog[apiError.Code]
	if !found {
		canonical = NewRiskAPIError(http.StatusInternalServerError, "STORAGE_FAILURE", "Risk review service is unavailable", true)
	}
	problemType := "urn:eco:problem:" + strings.ToLower(strings.ReplaceAll(canonical.Code, "_", "-"))
	writeProblem(c, Problem{Type: problemType, Title: canonical.Title, Status: canonical.Status, Code: canonical.Code, Retryable: canonical.Retryable, RequestID: requestID(c)})
}

var riskProblemCatalog = map[string]RiskAPIError{
	"RISK_IDEMPOTENCY_REQUIRED":        NewRiskAPIError(400, "RISK_IDEMPOTENCY_REQUIRED", "Idempotency-Key is required", false),
	"RISK_COMMAND_INVALID":             NewRiskAPIError(400, "RISK_COMMAND_INVALID", "Risk command is invalid", false),
	"RISK_REVISION_INVALID":            NewRiskAPIError(409, "RISK_REVISION_INVALID", "Risk revision identity is invalid", false),
	"RISK_BASELINE_INVALID":            NewRiskAPIError(409, "RISK_BASELINE_INVALID", "Risk baseline identity is invalid", false),
	"RISK_POLICY_INVALID":              NewRiskAPIError(409, "RISK_POLICY_INVALID", "Risk policy identity is invalid", false),
	"THRESHOLD_NOT_CONFIGURED":         NewRiskAPIError(409, "THRESHOLD_NOT_CONFIGURED", "Risk threshold is not configured", false),
	"THRESHOLD_INVALID":                NewRiskAPIError(422, "THRESHOLD_INVALID", "Risk threshold is invalid", false),
	"RISK_VALIDATION_INVALID":          NewRiskAPIError(409, "RISK_VALIDATION_INVALID", "Risk validation evidence is invalid", false),
	"RISK_SIMULATION_INVALID":          NewRiskAPIError(409, "RISK_SIMULATION_INVALID", "Risk simulation evidence is invalid", false),
	"RISK_COHORT_INVALID":              NewRiskAPIError(422, "RISK_COHORT_INVALID", "Risk cohort is invalid", false),
	"RISK_REQUIRED_METRIC_UNAVAILABLE": NewRiskAPIError(422, "RISK_REQUIRED_METRIC_UNAVAILABLE", "Required Metric is unavailable", false),
	"RISK_STALE_IDENTITY":              NewRiskAPIError(409, "RISK_STALE_IDENTITY", "Risk input identity is stale", false),
	"RISK_REGISTRY_INCOMPATIBLE":       NewRiskAPIError(409, "RISK_REGISTRY_INCOMPATIBLE", "Risk Registry is incompatible", false),
	"RISK_RULE_CONTRACT_INVALID":       NewRiskAPIError(409, "RISK_RULE_CONTRACT_INVALID", "Risk rule contract is invalid", false),
	"RISK_DECISION_INVALID":            NewRiskAPIError(400, "RISK_DECISION_INVALID", "Numeric risk decision is invalid", false),
	"RISK_REVIEW_NOT_FOUND":            NewRiskAPIError(404, "RISK_REVIEW_NOT_FOUND", "Risk review not found", false),
	"RISK_CANCELED":                    NewRiskAPIError(409, "RISK_CANCELED", "Risk evaluation was canceled", false),
	"RISK_INTERRUPTED":                 NewRiskAPIError(409, "RISK_INTERRUPTED", "Risk evaluation was interrupted", true),
	"RECOVERY_MISMATCH":                NewRiskAPIError(409, "RECOVERY_MISMATCH", "Simulation recovery mismatch", false),
	"RECOVERY_UNAVAILABLE":             NewRiskAPIError(409, "RECOVERY_UNAVAILABLE", "Simulation recovery unavailable", false),
	"IDEMPOTENCY_CONFLICT":             NewRiskAPIError(409, "IDEMPOTENCY_CONFLICT", "Idempotency key conflicts", false),
	"STORAGE_FAILURE":                  NewRiskAPIError(500, "STORAGE_FAILURE", "Storage failure", true),
	"BUDGET_EXCEEDED":                  NewRiskAPIError(422, "BUDGET_EXCEEDED", "Simulation budget exceeded", false),
	"TIMEOUT":                          NewRiskAPIError(504, "TIMEOUT", "Simulation timed out", false),
}

func writeRiskServiceError(c *gin.Context, err error) {
	var public RiskAPIError
	if errors.As(err, &public) {
		writeRiskError(c, public)
		return
	}
	switch {
	case errors.Is(err, store.ErrJobIdempotencyConflict), errors.Is(err, threshold.ErrIdempotencyConflict):
		public = NewRiskAPIError(http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key conflicts with another risk request", false)
	case errors.Is(err, threshold.ErrThresholdNotConfigured):
		public = NewRiskAPIError(http.StatusUnprocessableEntity, "THRESHOLD_NOT_CONFIGURED", "An enabled risk threshold is required", false)
	case errors.Is(err, threshold.ErrThresholdInvalid):
		public = NewRiskAPIError(http.StatusBadRequest, "THRESHOLD_INVALID", "Risk threshold selection is invalid", false)
	case errors.Is(err, orchestration.ErrRiskCanceled), errors.Is(err, context.Canceled):
		public = NewRiskAPIError(http.StatusConflict, "RISK_CANCELED", "Risk review was canceled", false)
	case errors.Is(err, orchestration.ErrRiskRecoveryMismatch):
		public = NewRiskAPIError(http.StatusConflict, "RECOVERY_MISMATCH", "Captured risk identities changed", false)
	case errors.Is(err, orchestration.ErrRiskRecoveryUnavailable):
		public = NewRiskAPIError(http.StatusServiceUnavailable, "RECOVERY_UNAVAILABLE", "Historical risk implementation is unavailable", true)
	case isRiskNotFound(err):
		public = NewRiskAPIError(http.StatusNotFound, "RISK_REVIEW_NOT_FOUND", "Risk review was not found", false)
	default:
		public = NewRiskAPIError(http.StatusInternalServerError, "STORAGE_FAILURE", "Risk review service is unavailable", true)
	}
	writeRiskError(c, public)
}

func isRiskNotFound(err error) bool {
	return errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrJobNotFound)
}
