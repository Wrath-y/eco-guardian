package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/app"
	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	"github.com/zouyi/eco-guardian/internal/validation"
)

type GraphSyncServiceProvider func() app.GraphSyncService

// GraphSyncServiceFromProjectManager is the sole HTTP composition seam for
// Graph admission. A missing active Store leaves the optional capability
// unavailable instead of creating a parallel project or Job implementation.
func GraphSyncServiceFromProjectManager(manager *project.Manager, submit func(context.Context, graphsync.GraphJob) error) GraphSyncServiceProvider {
	return GraphSyncServiceFromProjectManagerWithProvider(manager, nil, submit)
}

// GraphSyncServiceFromProjectManagerWithProvider optionally exposes exact
// read-only Snapshot observations to the status assembler. A nil provider
// produces an honest unavailable observation rather than a fallback read.
func GraphSyncServiceFromProjectManagerWithProvider(manager *project.Manager, provider graphsync.GraphProvider, submit func(context.Context, graphsync.GraphJob) error) GraphSyncServiceProvider {
	return func() app.GraphSyncService {
		handle, ok := manager.ActiveHandle()
		if !ok {
			return nil
		}
		storeProvider, ok := handle.(interface{ Store() *store.Store })
		if !ok {
			return nil
		}
		s := storeProvider.Store()
		return app.GraphSyncApplication{Revisions: s, Jobs: s, States: s, Summaries: s, Impact: s, Events: s, Validation: validation.NewValidationGate(s), Provider: provider, Submit: submit}
	}
}

// GraphHandler is a transport-only adapter for the generated Graph OpenAPI
// contract. It neither opens projects nor contacts local-rag directly.
type GraphHandler struct{ service GraphSyncServiceProvider }

func NewGraphHandler(service GraphSyncServiceProvider) *GraphHandler {
	return &GraphHandler{service: service}
}

func (h *GraphHandler) Register(r *gin.Engine) {
	r.POST("/api/v1/revisions/:id/graph-sync", h.ensure)
	r.GET("/api/v1/revisions/:id/graph-status", h.status)
}

func (h *GraphHandler) current(c *gin.Context) app.GraphSyncService {
	if h.service == nil {
		problem(c, http.StatusServiceUnavailable, "GRAPH_CAPABILITY_UNAVAILABLE", "Graph capability is unavailable")
		return nil
	}
	s := h.service()
	if s == nil {
		problem(c, http.StatusServiceUnavailable, "GRAPH_CAPABILITY_UNAVAILABLE", "Graph capability is unavailable")
	}
	return s
}

func (h *GraphHandler) ensure(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	revisionID, ok := idParam(c, "REVISION_NOT_FOUND")
	if !ok {
		return
	}
	var request struct {
		Intent       string    `json:"intent"`
		RetryOfJobID domain.ID `json:"retry_of_job_id"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		problem(c, http.StatusBadRequest, "GRAPH_RETRY_NOT_SAFE", "Invalid Graph sync request")
		return
	}
	var (
		job graphsync.GraphJob
		err error
	)
	switch request.Intent {
	case "ensure":
		if request.RetryOfJobID != "" {
			problem(c, http.StatusBadRequest, "GRAPH_RETRY_NOT_SAFE", "Ensure cannot include a retry Job")
			return
		}
		job, _, err = s.EnsureGraphSync(c.Request.Context(), revisionID)
	case "retry":
		key := c.GetHeader("Idempotency-Key")
		if !request.RetryOfJobID.Valid() || strings.TrimSpace(key) == "" {
			problem(c, http.StatusBadRequest, "GRAPH_RETRY_NOT_SAFE", "Retry requires a Job and Idempotency-Key")
			return
		}
		job, _, err = s.RetryGraphSync(c.Request.Context(), revisionID, request.RetryOfJobID, key)
	default:
		problem(c, http.StatusBadRequest, "GRAPH_RETRY_NOT_SAFE", "Graph sync intent must be ensure or retry")
		return
	}
	if err != nil {
		writeGraphAdmissionError(c, err)
		return
	}
	location := "/api/v1/jobs/" + string(job.ID)
	c.Header("Location", location)
	c.JSON(http.StatusAccepted, gin.H{"job": graphJobJSON(job), "location": location})
}

func (h *GraphHandler) status(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	revisionID, ok := idParam(c, "REVISION_NOT_FOUND")
	if !ok {
		return
	}
	status, err := s.GraphStatus(c.Request.Context(), revisionID)
	if err != nil {
		writeGraphAdmissionError(c, err)
		return
	}
	freshness := "unknown"
	if status.HasSyncState {
		freshness = "stale"
	}
	if status.Freshness.Fresh {
		freshness = "fresh"
	}
	response := gin.H{"revision_id": status.RevisionID, "config_hash": status.ConfigHash, "pipeline_state": status.Pipeline, "freshness": freshness, "freshness_reasons": status.Freshness.Reasons, "validation_result": status.Validation, "impact_state": nullable(status.ImpactState), "warnings": graphWarningsJSON(status.Warnings), "actions": graphActions(status), "evidence": []gin.H{{"key": "config_hash", "value": status.ConfigHash}}}
	if status.Summary != nil {
		response["projection"] = gin.H{"projection_schema_version": status.Summary.SchemaVersion, "projector_version": status.Summary.ProjectorVersion, "graph_manifest_hash": status.Summary.ManifestHash, "node_count": status.Summary.NodeCount, "edge_count": status.Summary.EdgeCount}
	} else {
		response["projection"] = nil
	}
	if status.Job != nil {
		response["job"] = graphJobJSON(*status.Job)
	} else {
		response["job"] = nil
	}
	if status.JobProgress != nil {
		response["job_phase"] = status.JobPhase
		response["job_progress"] = *status.JobProgress
	}
	if status.Provider != nil {
		response["provider"] = graphProviderJSON(*status.Provider, status.ProviderObservedAt)
	} else {
		response["provider"] = nil
	}
	if status.SafeError != "" {
		response["error"] = gin.H{"code": status.SafeError, "retryable": status.Pipeline == graphsync.StateFailed}
	} else if status.ProviderError != nil {
		response["error"] = gin.H{"code": status.ProviderError.Code, "retryable": status.ProviderError.Retryable, "request_id": status.ProviderError.RequestID, "provider_code": status.ProviderError.Code}
	} else {
		response["error"] = nil
	}
	c.JSON(http.StatusOK, response)
}

func graphProviderJSON(snapshot graphsync.Snapshot, observedAt time.Time) gin.H {
	components := make([]gin.H, 0, len(snapshot.Components))
	for _, component := range snapshot.Components {
		switch component.Name {
		case "graph", "fts", "vector", "rerank":
			components = append(components, gin.H{"name": component.Name, "state": graphComponentState(component.State)})
		}
	}
	return gin.H{"namespace": snapshot.Namespace, "version": snapshot.Version, "task_id": nullable(snapshot.TaskID), "status": graphSnapshotState(snapshot.Status), "query_ready": snapshot.QueryReady, "components": components, "observed_at": observedAt}
}

func graphSnapshotState(value string) string {
	switch value {
	case "queued", "building", "ready", "failed", "unavailable":
		return value
	default:
		return "unknown"
	}
}

func graphComponentState(value string) string {
	switch value {
	case "ready", "building", "failed", "unavailable":
		return value
	default:
		return "unknown"
	}
}

func graphWarningsJSON(warnings []string) []gin.H {
	result := make([]gin.H, 0, len(warnings))
	for _, warning := range warnings {
		result = append(result, gin.H{"code": warning, "message": warning})
	}
	return result
}

func graphActions(status app.GraphStatus) []string {
	switch status.Pipeline {
	case graphsync.StateQueued, graphsync.StateBuilding:
		return []string{"wait"}
	case graphsync.StateFailed:
		return []string{"retry", "inspect"}
	case graphsync.StateReady:
		return []string{"none"}
	default:
		return []string{"inspect"}
	}
}

func graphJobJSON(job graphsync.GraphJob) gin.H {
	response := gin.H{"id": job.ID, "kind": "graph_sync", "revision_id": job.RevisionID, "status": job.Status, "request_hash": job.RequestHash, "events_url": "/api/v1/jobs/" + string(job.ID) + "/events", "poll_after_ms": 1000, "cancel_generation": job.CancelGeneration, "cancel_requested_at": job.CancelRequestedAt, "created_at": job.CreatedAt, "updated_at": job.UpdatedAt}
	if job.Result != nil {
		response["result_type"] = job.Result.Type
		response["result_id"] = job.Result.ID
		response["result_url"] = job.Result.URL
	}
	return response
}

func writeGraphAdmissionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, app.ErrGraphRevisionUnavailable):
		problem(c, http.StatusNotFound, "REVISION_NOT_FOUND", "Revision not found")
	case errors.Is(err, app.ErrGraphRetryInvalid):
		problem(c, http.StatusConflict, "GRAPH_RETRY_NOT_SAFE", "Graph retry is not safe")
	case errors.Is(err, app.ErrGraphOperationUnavailable):
		problem(c, http.StatusServiceUnavailable, "GRAPH_CAPABILITY_UNAVAILABLE", "Graph capability is unavailable")
	case errors.Is(err, graphsync.ErrValidationNotPassed):
		problem(c, http.StatusConflict, "GRAPH_VALIDATION_REQUIRED", "Graph sync requires an exact passing full validation")
	default:
		problem(c, http.StatusServiceUnavailable, "GRAPH_CAPABILITY_UNAVAILABLE", "Graph sync admission is unavailable")
	}
}
