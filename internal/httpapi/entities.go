package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/backup/application"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type EntityStore interface {
	Get(context.Context, domain.EntityKind, domain.ID) (domain.Entity, error)
	List(context.Context, domain.EntityKind, string, string, int) (store.Page, error)
	Create(context.Context, domain.EntityKind, domain.EntityDraft) (domain.Entity, domain.RevisionSummary, error)
	Patch(context.Context, domain.EntityKind, domain.ID, int64, domain.EntityPatch) (domain.Entity, domain.RevisionSummary, error)
	Delete(context.Context, domain.EntityKind, domain.ID, int64) (domain.Entity, domain.RevisionSummary, error)
}
type StoreProvider func() EntityStore
type EntityHandler struct{ store StoreProvider }

func NewEntityHandler(store StoreProvider) *EntityHandler { return &EntityHandler{store: store} }
func StoreFromProjectManager(manager *project.Manager) StoreProvider {
	return func() EntityStore {
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
}
func (h *EntityHandler) Register(r *gin.Engine) {
	r.GET("/api/v1/entities/:kind", h.list)
	r.POST("/api/v1/entities/:kind", h.create)
	r.GET("/api/v1/entities/:kind/:id", h.get)
	r.PATCH("/api/v1/entities/:kind/:id", h.patch)
	r.DELETE("/api/v1/entities/:kind/:id", h.delete)
}
func (h *EntityHandler) current(c *gin.Context) EntityStore {
	s := h.store()
	if s == nil {
		problem(c, 404, "PROJECT_NOT_OPEN", "No active project")
	}
	return s
}
func entityETag(e domain.Entity) string { return fmt.Sprintf(`"%s:%d"`, e.ID, e.EntityVersion) }
func parseETag(value string, id domain.ID) (int64, bool) {
	value = strings.Trim(value, `"`)
	parts := strings.Split(value, ":")
	if len(parts) != 2 || parts[0] != string(id) {
		return 0, false
	}
	v, err := strconv.ParseInt(parts[1], 10, 64)
	return v, err == nil && v > 0
}
func kindID(c *gin.Context) (domain.EntityKind, domain.ID, bool) {
	k := domain.EntityKind(c.Param("kind"))
	id := domain.ID(c.Param("id"))
	if !k.Valid() {
		problem(c, 400, "UNSUPPORTED_KIND", "Unsupported entity kind")
		return "", "", false
	}
	if !id.Valid() {
		problem(c, 404, "VALIDATION_FAILED", "Invalid entity ID")
		return "", "", false
	}
	return k, id, true
}
func (h *EntityHandler) list(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	k := domain.EntityKind(c.Param("kind"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if !k.Valid() {
		problem(c, 400, "UNSUPPORTED_KIND", "Unsupported entity kind")
		return
	}
	if limit < 1 || limit > 200 {
		problem(c, 400, "VALIDATION_FAILED", "limit must be between 1 and 200")
		return
	}
	p, err := s.List(c.Request.Context(), k, c.Query("query"), c.Query("cursor"), limit)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(200, gin.H{"items": p.Items, "next_cursor": nullable(p.NextCursor)})
}
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (h *EntityHandler) create(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	k := domain.EntityKind(c.Param("kind"))
	var d domain.EntityDraft
	if err := c.ShouldBindJSON(&d); err != nil {
		problem(c, 400, "VALIDATION_FAILED", "Invalid entity request")
		return
	}
	e, r, err := s.Create(c.Request.Context(), k, d)
	if err != nil {
		writeError(c, err)
		return
	}
	c.Header("ETag", entityETag(e))
	c.JSON(201, gin.H{"entity": e, "revision": r})
}
func (h *EntityHandler) get(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	k, id, ok := kindID(c)
	if !ok {
		return
	}
	e, err := s.Get(c.Request.Context(), k, id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.Header("ETag", entityETag(e))
	c.JSON(200, e)
}
func (h *EntityHandler) patch(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	k, id, ok := kindID(c)
	if !ok {
		return
	}
	version, ok := parseETag(c.GetHeader("If-Match"), id)
	if !ok {
		problem(c, 428, "PRECONDITION_REQUIRED", "If-Match is required")
		return
	}
	var p domain.EntityPatch
	if err := json.NewDecoder(c.Request.Body).Decode(&p); err != nil {
		problem(c, 400, "VALIDATION_FAILED", "Invalid patch")
		return
	}
	e, r, err := s.Patch(c.Request.Context(), k, id, version, p)
	if err != nil {
		writeError(c, err)
		return
	}
	c.Header("ETag", entityETag(e))
	c.JSON(200, gin.H{"entity": e, "revision": r})
}
func (h *EntityHandler) delete(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	k, id, ok := kindID(c)
	if !ok {
		return
	}
	version, ok := parseETag(c.GetHeader("If-Match"), id)
	if !ok {
		problem(c, 428, "PRECONDITION_REQUIRED", "If-Match is required")
		return
	}
	e, r, err := s.Delete(c.Request.Context(), k, id, version)
	if err != nil {
		writeError(c, err)
		return
	}
	c.Header("ETag", entityETag(e))
	c.JSON(200, gin.H{"entity": e, "revision": r})
}
func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrDailyBackupRequired):
		var required application.DailyRequiredError
		if errors.As(err, &required) {
			problemDetails(c, http.StatusConflict, "DAILY_BACKUP_REQUIRED", "Daily backup failed; the edit was not committed", gin.H{"failed_backup_job_id": required.JobID, "state": required.State}, "")
			return
		}
		problem(c, http.StatusConflict, "DAILY_BACKUP_REQUIRED", "Daily backup is required before editing")
	case errors.Is(err, store.ErrNotFound):
		problem(c, 404, "VALIDATION_FAILED", "Entity not found")
	case errors.Is(err, store.ErrDuplicateKey):
		problem(c, 409, "DUPLICATE_KEY", "Duplicate entity key")
	case errors.Is(err, store.ErrRevisionConflict):
		problem(c, 409, "REVISION_CONFLICT", "Entity revision conflict")
	default:
		var ref *store.ReferencedError
		if errors.As(err, &ref) {
			problemDetails(c, 409, "ENTITY_REFERENCED", "Entity is referenced", gin.H{"references": ref.References}, "")
			return
		}
		var validation store.ValidationError
		if errors.As(err, &validation) {
			field := ""
			if len(validation.Issues) > 0 {
				field = validation.Issues[0].Path
			}
			problemDetails(c, 400, "VALIDATION_FAILED", "Entity validation failed", gin.H{"issues": validation.Issues}, field)
			return
		}
		problem(c, 500, "VALIDATION_FAILED", "Internal storage error")
	}
}
