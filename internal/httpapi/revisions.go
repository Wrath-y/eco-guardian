package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/app"
	"github.com/zouyi/eco-guardian/internal/domain"
	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	versioningpolicy "github.com/zouyi/eco-guardian/internal/versioning/policy"
	versioningrelease "github.com/zouyi/eco-guardian/internal/versioning/release"
	versioningrevision "github.com/zouyi/eco-guardian/internal/versioning/revision"
)

type VersionServiceProvider func() app.VersioningService
type VersionHandler struct {
	service   VersionServiceProvider
	graph     GraphSyncServiceProvider
	resolvers []DurableResolver
}

// VersioningServiceFromProjectManager is composition glue only. Handlers see
// the app service; this function is the sole project-manager path that knows
// how the active project exposes its store ports.
func VersioningServiceFromProjectManager(manager *project.Manager) VersionServiceProvider {
	return VersioningServiceFromProjectManagerWithDependencies(manager, app.VersioningDependencies{})
}

// VersioningServiceFromProjectManagerWithDependencies lets the composition
// root provide its registered Gate catalog and release worker callbacks
// without widening HTTP handlers or project handles.
func VersioningServiceFromProjectManagerWithDependencies(manager *project.Manager, dependencies app.VersioningDependencies) VersionServiceProvider {
	return func() app.VersioningService {
		handle, ok := manager.ActiveHandle()
		if !ok {
			return nil
		}
		provider, ok := handle.(interface{ Store() *store.Store })
		if !ok {
			return nil
		}
		s := provider.Store()
		return app.VersioningApplication{Revisions: s, Diff: s, Policies: s, Releases: s, Jobs: s, Catalog: dependencies.Catalog, Registry: dependencies.Registry, Submit: dependencies.Submit, Cancel: dependencies.Cancel, GraphRuntime: dependencies.GraphRuntime}
	}
}

func NewVersionHandler(service VersionServiceProvider) *VersionHandler {
	return &VersionHandler{service: service, resolvers: []DurableResolver{releaseResolver(service)}}
}

// NewVersionHandlerWithGraph joins the two existing application services at
// the shared durable Job transport boundary. Graph jobs deliberately retain
// the same /jobs resource, SSE protocol, and cancellation semantics as
// release jobs rather than creating a second public job API.
func NewVersionHandlerWithGraph(service VersionServiceProvider, graph GraphSyncServiceProvider) *VersionHandler {
	return &VersionHandler{service: service, graph: graph, resolvers: []DurableResolver{releaseResolver(service), graphResolver(graph)}}
}

// RegisterDurableResolver composes another capability into the shared Job
// transport without changing its public routes or event protocol.
func (h *VersionHandler) RegisterDurableResolver(resolver DurableResolver) {
	if resolver != nil {
		h.resolvers = append(h.resolvers, resolver)
	}
}

func releaseResolver(provider VersionServiceProvider) DurableResolverFunc {
	return DurableResolverFunc{
		Get: func(ctx context.Context, id domain.ID) (map[string]any, bool, error) {
			if service := provider(); service != nil {
				if job, err := service.GetReleaseJob(ctx, id); err == nil {
					return jobJSON(job), true, nil
				}
			}
			return nil, false, nil
		},
		Cancel: func(ctx context.Context, id domain.ID) (map[string]any, bool, bool, error) {
			if service := provider(); service != nil {
				job, changed, err := service.CancelReleaseJob(ctx, id)
				if err == nil {
					return jobJSON(job), true, changed, nil
				}
			}
			return nil, false, false, nil
		},
		Events: func(ctx context.Context, id domain.ID, after int64) ([]DurableJobEvent, error) {
			if service := provider(); service != nil {
				events, err := service.ListReleaseJobEvents(ctx, id, after)
				if err != nil {
					return nil, err
				}
				result := make([]DurableJobEvent, 0, len(events))
				for _, event := range events {
					result = append(result, DurableJobEvent{Ordinal: event.Ordinal, Payload: event})
				}
				return result, nil
			}
			return nil, nil
		},
	}
}

func graphResolver(provider GraphSyncServiceProvider) DurableResolverFunc {
	return DurableResolverFunc{
		Get: func(ctx context.Context, id domain.ID) (map[string]any, bool, error) {
			if service := provider(); service != nil {
				if job, err := service.GetGraphJob(ctx, id); err == nil {
					return graphJobJSON(job), true, nil
				}
			}
			return nil, false, nil
		},
		Cancel: func(ctx context.Context, id domain.ID) (map[string]any, bool, bool, error) {
			if service := provider(); service != nil {
				job, changed, err := service.CancelGraphJob(ctx, id)
				if err == nil {
					return graphJobJSON(job), true, changed, nil
				}
			}
			return nil, false, false, nil
		},
		Events: func(ctx context.Context, id domain.ID, after int64) ([]DurableJobEvent, error) {
			if service := provider(); service != nil {
				events, err := service.ListGraphJobEvents(ctx, id, after)
				if err != nil {
					return nil, err
				}
				result := make([]DurableJobEvent, 0, len(events))
				for _, event := range events {
					result = append(result, DurableJobEvent{Ordinal: event.Ordinal, Payload: graphEventJSON(event)})
				}
				return result, nil
			}
			return nil, nil
		},
	}
}
func (h *VersionHandler) Register(r *gin.Engine) {
	r.HandleMethodNotAllowed = true
	r.NoMethod(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/v1/revisions") || strings.HasPrefix(c.Request.URL.Path, "/api/v1/release-policies") || strings.HasPrefix(c.Request.URL.Path, "/api/v1/releases") || strings.HasPrefix(c.Request.URL.Path, "/api/v1/jobs") {
			problem(c, http.StatusMethodNotAllowed, "REVISION_IMMUTABLE", "This versioning resource is read-only")
			return
		}
		problem(c, http.StatusNotFound, "HISTORY_NOT_FOUND", "Resource not found")
	})
	r.GET("/api/v1/revisions", h.list)
	r.POST("/api/v1/revisions", h.createRevision)
	r.GET("/api/v1/revisions/:id", h.detail)
	r.GET("/api/v1/revisions/:id/diff", h.diff)
	r.GET("/api/v1/release-policies", h.listPolicies)
	r.POST("/api/v1/release-policies", h.createPolicy)
	r.GET("/api/v1/releases", h.listReleases)
	r.POST("/api/v1/releases", h.createRelease)
	r.GET("/api/v1/releases/:id", h.releaseDetail)
	r.GET("/api/v1/jobs/:id", h.job)
	r.GET("/api/v1/jobs/:id/events", h.events)
	r.POST("/api/v1/jobs/:id/cancel", h.cancel)
	r.GET("/api/v1/runtime/capabilities", h.capability)
}
func (h *VersionHandler) current(c *gin.Context) app.VersioningService {
	s := h.service()
	if s == nil {
		problem(c, http.StatusNotFound, "PROJECT_NOT_OPEN", "No active project")
	}
	return s
}
func (h *VersionHandler) graphCurrent() app.GraphSyncService {
	if h.graph == nil {
		return nil
	}
	return h.graph()
}
func pageLimit(c *gin.Context) (int, bool) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 || limit > 200 {
		problem(c, http.StatusBadRequest, "VALIDATION_FAILED", "limit must be between 1 and 200")
		return 0, false
	}
	return limit, true
}
func idParam(c *gin.Context, code string) (domain.ID, bool) {
	id := domain.ID(c.Param("id"))
	if !id.Valid() {
		problem(c, http.StatusNotFound, code, "Resource not found")
		return "", false
	}
	return id, true
}

func revisionJSON(record versioningrevision.Record, status []string) gin.H {
	metadata := record.Metadata
	return gin.H{"id": metadata.RevisionID, "display_revision": record.DisplayRevision, "config_hash": metadata.ConfigHash,
		"metadata": gin.H{"revision_id": metadata.RevisionID, "config_hash": metadata.ConfigHash, "name": nullable(metadata.Name), "description": nullable(metadata.Description), "parent_revision_id": nullable(string(metadata.ParentRevisionID)), "source_revision_id": nullable(string(metadata.SourceRevisionID)), "source_release_id": nullable(string(metadata.SourceReleaseID)), "version_manifest": gin.H{"entries": metadata.Manifest.Entries, "hash": metadata.ManifestHash}, "created_at": metadata.CreatedAt},
		"status":   status}
}
func (h *VersionHandler) list(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	limit, ok := pageLimit(c)
	if !ok {
		return
	}
	page, err := s.ListRevisionRecords(c.Request.Context(), c.Query("cursor"), limit)
	if err != nil {
		problem(c, http.StatusBadRequest, "REVISION_HISTORY_INVALID", "Revision history is unavailable")
		return
	}
	items := make([]gin.H, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, revisionJSON(record, []string{"history"}))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": nullable(page.NextCursor)})
}
func (h *VersionHandler) detail(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	id, ok := idParam(c, "REVISION_NOT_FOUND")
	if !ok {
		return
	}
	detail, err := s.GetRevisionDetail(c.Request.Context(), id)
	if err != nil {
		problem(c, http.StatusNotFound, "REVISION_NOT_FOUND", "Revision not found")
		return
	}
	status := []string{"history"}
	if detail.ActiveReleaseID != "" {
		status = append(status, "active_release")
	}
	response := revisionJSON(detail.Record, status)
	response["timeline"] = detail.Timeline
	c.JSON(http.StatusOK, response)
}
func (h *VersionHandler) createRevision(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	var request struct {
		Kind           string `json:"kind"`
		CurrentWorking struct {
			RevisionID domain.ID `json:"revision_id"`
		} `json:"current_working"`
		Name            string    `json:"name"`
		Description     string    `json:"description"`
		SourceReleaseID domain.ID `json:"source_release_id"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || !request.CurrentWorking.RevisionID.Valid() {
		problem(c, http.StatusBadRequest, "REVISION_CONFLICT", "A current revision is required")
		return
	}
	var created domain.RevisionSummary
	var err error
	switch request.Kind {
	case "checkpoint":
		created, err = s.CreateCheckpoint(c.Request.Context(), request.CurrentWorking.RevisionID, request.Name, request.Description)
	case "restore_release":
		if !request.SourceReleaseID.Valid() {
			problem(c, http.StatusBadRequest, "REVISION_CONFLICT", "A source release is required")
			return
		}
		created, err = s.RestoreRelease(c.Request.Context(), request.CurrentWorking.RevisionID, request.SourceReleaseID)
	default:
		problem(c, http.StatusBadRequest, "VALIDATION_FAILED", "Unsupported revision command")
		return
	}
	if err != nil {
		problem(c, http.StatusConflict, "REVISION_CONFLICT", "Revision could not be created")
		return
	}
	detail, err := s.GetRevisionDetail(c.Request.Context(), created.ID)
	if err != nil {
		problem(c, http.StatusInternalServerError, "STORAGE_FAILURE", "Revision was created but cannot be read")
		return
	}
	response := revisionJSON(detail.Record, []string{"history"})
	response["timeline"] = detail.Timeline
	c.JSON(http.StatusCreated, response)
}
func (h *VersionHandler) diff(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	target, ok := idParam(c, "REVISION_NOT_FOUND")
	if !ok {
		return
	}
	base := domain.ID(c.Query("base"))
	if !base.Valid() {
		problem(c, http.StatusBadRequest, "DIFF_BASE_INVALID", "A valid immutable base revision is required")
		return
	}
	changes, err := s.CompareRevisions(c.Request.Context(), base, target)
	if err != nil {
		problem(c, http.StatusBadRequest, "DIFF_BASE_INVALID", "Diff base is unavailable")
		return
	}
	baseDetail, baseErr := s.GetRevisionDetail(c.Request.Context(), base)
	targetDetail, targetErr := s.GetRevisionDetail(c.Request.Context(), target)
	if baseErr != nil || targetErr != nil {
		problem(c, http.StatusBadRequest, "DIFF_BASE_INVALID", "Diff revisions are unavailable")
		return
	}
	c.JSON(http.StatusOK, gin.H{"base_revision_id": base, "target_revision_id": target, "base_config_hash": baseDetail.Record.Metadata.ConfigHash, "target_config_hash": targetDetail.Record.Metadata.ConfigHash, "baseline_state": "AVAILABLE", "changes": changes})
}

func policyJSON(policy versioningpolicy.ReleasePolicy) gin.H {
	return gin.H{"id": policy.ID, "display_version": policy.DisplayVersion, "scenes": policy.Scenes, "samples": policy.Samples, "threshold_id": policy.ThresholdID, "threshold_enabled": policy.ThresholdOn, "capabilities": policy.Capabilities, "policy_hash": policy.CanonicalHash, "created_at": policy.CreatedAt}
}
func (h *VersionHandler) listPolicies(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	limit, ok := pageLimit(c)
	if !ok {
		return
	}
	page, err := s.ListPolicies(c.Request.Context(), c.Query("cursor"), limit)
	if err != nil {
		problem(c, http.StatusBadRequest, "RELEASE_POLICY_INVALID", "Release policy history is unavailable")
		return
	}
	items := make([]gin.H, 0, len(page.Items))
	for _, policy := range page.Items {
		items = append(items, policyJSON(policy))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": nullable(page.NextCursor)})
}
func (h *VersionHandler) createPolicy(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	var definition versioningpolicy.Definition
	if err := c.ShouldBindJSON(&definition); err != nil {
		problem(c, http.StatusBadRequest, "RELEASE_POLICY_INVALID", "Invalid release policy")
		return
	}
	policy, err := s.CreatePolicy(c.Request.Context(), definition)
	if err != nil {
		problem(c, http.StatusBadRequest, "RELEASE_POLICY_INVALID", "Release policy could not be created")
		return
	}
	c.JSON(http.StatusCreated, policyJSON(policy))
}

func releaseJSON(record versioningrelease.ReadRecord) gin.H {
	return gin.H{"id": record.ID, "revision_id": record.RevisionID, "policy_id": record.PolicyID, "baseline_release_id": nullable(string(record.BaselineReleaseID)), "intent_id": record.IntentID, "notes": record.Notes, "gate_evidence": json.RawMessage(record.GateEvidence), "confirmations": record.Confirmations, "created_at": record.CreatedAt}
}
func (h *VersionHandler) listReleases(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	limit, ok := pageLimit(c)
	if !ok {
		return
	}
	page, err := s.ListReleaseRecords(c.Request.Context(), c.Query("cursor"), limit)
	if err != nil {
		problem(c, http.StatusBadRequest, "HISTORY_NOT_FOUND", "Release history is unavailable")
		return
	}
	items := make([]gin.H, 0, len(page.Items))
	for _, release := range page.Items {
		items = append(items, releaseJSON(release))
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "next_cursor": nullable(page.NextCursor)})
}
func (h *VersionHandler) releaseDetail(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	id, ok := idParam(c, "HISTORY_NOT_FOUND")
	if !ok {
		return
	}
	detail, err := s.GetReleaseRecord(c.Request.Context(), id)
	if err != nil {
		problem(c, http.StatusNotFound, "HISTORY_NOT_FOUND", "Release not found")
		return
	}
	response := releaseJSON(detail.Record)
	response["active_pointer"] = detail.Pointer
	c.JSON(http.StatusOK, response)
}
func (h *VersionHandler) createRelease(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	var payload map[string]json.RawMessage
	if err := c.ShouldBindJSON(&payload); err != nil {
		problem(c, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid release request")
		return
	}
	baseline, specified := payload["expected_baseline_release_id"]
	var command versioningrelease.Command
	if err := json.Unmarshal(mustJSON(payload), &command); err != nil || !specified {
		problem(c, http.StatusBadRequest, "VALIDATION_FAILED", "Invalid release request")
		return
	}
	command.BaselineSpecified = true
	if string(baseline) == "null" {
		command.BaselineReleaseID = ""
	}
	command.IdempotencyKey = c.GetHeader("Idempotency-Key")
	if !command.Valid() {
		problem(c, http.StatusBadRequest, "INVALID_CONFIRMATION", "Release request or Idempotency-Key is invalid")
		return
	}
	job, err := s.CreateRelease(c.Request.Context(), command)
	if err != nil {
		writeReleaseError(c, err)
		return
	}
	location := "/api/v1/jobs/" + string(job.ID)
	c.Header("Location", location)
	c.JSON(http.StatusAccepted, gin.H{"job_id": job.ID, "location": location})
}

func writeReleaseError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrIdempotencyConflict):
		problem(c, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency-Key conflicts with an existing release request")
	case errors.Is(err, versioningrelease.ErrReleaseBaseConflict):
		problem(c, http.StatusConflict, "RELEASE_BASE_CONFLICT", "Expected release baseline is no longer current")
	case errors.Is(err, versioningrelease.ErrReleaseCapabilityDisabled):
		problem(c, http.StatusConflict, "RELEASE_CAPABILITY_DISABLED", "Required release capabilities are unavailable")
	case errors.Is(err, versioningrelease.ErrBaselineConfirmation), errors.Is(err, versioningrelease.ErrWarningConfirmation), errors.Is(err, versioningrelease.ErrOverrideInvalid), errors.Is(err, versioningrelease.ErrCommandInvalid):
		problem(c, http.StatusBadRequest, "INVALID_CONFIRMATION", "Release confirmation is invalid")
	case errors.Is(err, versioningrelease.ErrPreflightUnavailable), errors.Is(err, app.ErrVersioningOperationUnavailable):
		problem(c, http.StatusServiceUnavailable, "EXTERNAL_SERVICE_FAILURE", "Release dependency is unavailable")
	default:
		problem(c, http.StatusUnprocessableEntity, "RELEASE_PREFLIGHT_FAILED", "Release preflight failed")
	}
}
func mustJSON(value any) []byte { body, _ := json.Marshal(value); return body }
func jobJSON(job versioningrelease.Job) gin.H {
	response := gin.H{"id": job.ID, "kind": "release", "revision_id": job.RevisionID, "status": job.Status, "request_hash": job.RequestHash, "events_url": "/api/v1/jobs/" + string(job.ID) + "/events", "cancel_generation": job.CancelGeneration, "cancel_requested_at": job.CancelRequestedAt, "created_at": job.CreatedAt, "updated_at": job.UpdatedAt, "poll_after_ms": 1000}
	if job.Result != nil {
		response["result_type"] = job.Result.Type
		response["result_id"] = job.Result.ID
		response["result_url"] = job.Result.URL
	}
	return response
}
func (h *VersionHandler) job(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	id, ok := idParam(c, "HISTORY_NOT_FOUND")
	if !ok {
		return
	}
	for _, resolver := range h.resolvers {
		if job, found, err := resolver.GetJob(c.Request.Context(), id); err == nil && found {
			c.JSON(http.StatusOK, job)
			return
		}
	}
	problem(c, http.StatusNotFound, "HISTORY_NOT_FOUND", "Job not found")
}
func (h *VersionHandler) cancel(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	id, ok := idParam(c, "HISTORY_NOT_FOUND")
	if !ok {
		return
	}
	for _, resolver := range h.resolvers {
		if job, found, canceled, err := resolver.CancelJob(c.Request.Context(), id); err == nil && found {
			if !canceled {
				problem(c, http.StatusConflict, "RELEASE_PREFLIGHT_FAILED", "Job is already terminal")
				return
			}
			c.JSON(http.StatusAccepted, job)
			return
		}
	}
	problem(c, http.StatusConflict, "RELEASE_PREFLIGHT_FAILED", "Job cannot be canceled")
}
func (h *VersionHandler) events(c *gin.Context) {
	id, ok := idParam(c, "HISTORY_NOT_FOUND")
	if !ok {
		return
	}
	after := int64(0)
	if raw := c.GetHeader("Last-Event-ID"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 0 {
			problem(c, http.StatusBadRequest, "VALIDATION_FAILED", "Last-Event-ID must be a non-negative ordinal")
			return
		}
		after = value
	}
	for _, resolver := range h.resolvers {
		if _, found, err := resolver.GetJob(c.Request.Context(), id); err == nil && found {
			h.streamJobEvents(c, resolver, id, after)
			return
		}
	}
	problem(c, http.StatusNotFound, "HISTORY_NOT_FOUND", "Job not found")
}

func streamHeaders(c *gin.Context) (interface{ Flush() }, bool) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	flusher, ok := c.Writer.(interface{ Flush() })
	if !ok {
		problem(c, http.StatusInternalServerError, "STORAGE_FAILURE", "Streaming is unavailable")
	}
	return flusher, ok
}

func terminalJobProjection(job map[string]any) bool {
	switch fmt.Sprint(job["status"]) {
	case "succeeded", "failed", "canceled", "interrupted":
		return true
	default:
		return false
	}
}

func (h *VersionHandler) streamJobEvents(c *gin.Context, resolver DurableResolver, id domain.ID, after int64) {
	flusher, ok := streamHeaders(c)
	if !ok {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		events, err := resolver.ListJobEvents(c.Request.Context(), id, after)
		if err != nil {
			return
		}
		for _, event := range events {
			payload, _ := json.Marshal(event.Payload)
			_, _ = fmt.Fprintf(c.Writer, "id: %d\nevent: job\ndata: %s\n\n", event.Ordinal, payload)
			after = event.Ordinal
		}
		job, found, err := resolver.GetJob(c.Request.Context(), id)
		if err != nil || !found {
			return
		}
		if terminalJobProjection(job) {
			payload, _ := json.Marshal(job)
			_, _ = fmt.Fprintf(c.Writer, "event: terminal\ndata: %s\n\n", payload)
			flusher.Flush()
			return
		}
		_, _ = fmt.Fprint(c.Writer, ": heartbeat\n\n")
		flusher.Flush()
		select {
		case <-c.Request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func graphEventJSON(event graphsync.GraphJobEvent) gin.H {
	response := gin.H{"job_id": event.JobID, "ordinal": event.Ordinal, "phase": event.Phase, "progress": event.Progress, "created_at": time.Now().UTC()}
	if event.Warning != "" {
		response["warning"] = event.Warning
	}
	if event.SafeError != "" {
		response["error"] = event.SafeError
	}
	if event.Result != nil {
		response["result_type"] = event.Result.Type
		response["result_id"] = event.Result.ID
		response["result_url"] = event.Result.URL
	}
	return response
}
func (h *VersionHandler) capability(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	capability, err := s.ReleaseCapability(c.Request.Context())
	if err != nil {
		problem(c, http.StatusServiceUnavailable, "EXTERNAL_SERVICE_FAILURE", "Runtime capability is unavailable")
		return
	}
	reasons := make([]gin.H, 0, len(capability.Reasons))
	for _, reason := range capability.Reasons {
		code := "UNAVAILABLE"
		if strings.Contains(reason.Reason, "unregistered") {
			code = "MISSING"
		}
		if strings.Contains(reason.Reason, "incompatible") {
			code = "INCOMPATIBLE"
		}
		reasons = append(reasons, gin.H{"capability_id": reason.CapabilityID, "gate_id": reason.GateID, "code": code, "detail": nullable(reason.Reason)})
	}
	graph := s.GraphRuntimeCapability(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{"release": gin.H{"enabled": capability.Enabled, "disabled_reasons": reasons}, "graph": gin.H{"available": graph.Available, "compatible": graph.Compatible, "required_capabilities": graph.RequiredCapabilities, "degradations": graph.Degradations, "disabled_reasons": graph.Reasons, "release_disabled_reasons": graph.Reasons, "observed_at": graph.ObservedAt}})
}
