package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type ProjectHandler struct {
	manager            *project.Manager
	selector           project.DirectorySelector
	closer             project.OtherInstanceCloser
	projectStateChange func(active bool)
}

func NewProjectHandler(manager *project.Manager, selector project.DirectorySelector, closers ...project.OtherInstanceCloser) *ProjectHandler {
	var closer project.OtherInstanceCloser
	if len(closers) > 0 {
		closer = closers[0]
	}
	return &ProjectHandler{manager: manager, selector: selector, closer: closer}
}

// ObserveProjectStateChanges registers a process-local notification after a
// successful project open, create, or close so derived runtime projections
// can converge without waiting for a manual retry.
func (h *ProjectHandler) ObserveProjectStateChanges(observer func(active bool)) *ProjectHandler {
	if h != nil {
		h.projectStateChange = observer
	}
	return h
}

func (h *ProjectHandler) notifyProjectStateChange(active bool) {
	if h != nil && h.projectStateChange != nil {
		h.projectStateChange(active)
	}
}

func (h *ProjectHandler) Register(r *gin.Engine) {
	r.POST("/api/v1/project-selections", h.selectDir)
	r.POST("/api/v1/projects", h.open)
	r.GET("/api/v1/projects/recent", h.recent)
	r.POST("/api/v1/projects/recent/:id", h.openRecent)
	r.POST("/api/v1/projects/recent/:id/close-other-instance", h.closeOtherInstance)
	r.GET("/api/v1/projects/current", h.current)
	r.POST("/api/v1/projects/close", h.close)
}
func (h *ProjectHandler) closeOtherInstance(c *gin.Context) {
	if !safeLoopbackOrigin(c.Request) {
		problem(c, http.StatusForbidden, "OTHER_INSTANCE_UNAVAILABLE", "Request origin is not allowed")
		return
	}
	id := domain.ID(c.Param("id"))
	if !id.Valid() {
		problem(c, http.StatusBadRequest, "INVALID_SELECTION", "Invalid recent project")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := h.manager.CloseOther(ctx, id, h.closer); err != nil {
		writeProjectError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
func (h *ProjectHandler) recent(c *gin.Context) {
	values, err := h.manager.Recent()
	if err != nil {
		writeProjectError(c, err)
		return
	}
	// ProjectInfo deliberately omits Path from JSON.
	c.JSON(http.StatusOK, values)
}
func (h *ProjectHandler) openRecent(c *gin.Context) {
	id := domain.ID(c.Param("id"))
	if !id.Valid() {
		problem(c, 400, "INVALID_SELECTION", "Invalid recent project")
		return
	}
	info, err := h.manager.OpenRecent(c.Request.Context(), id)
	if err != nil {
		writeProjectError(c, err)
		return
	}
	h.notifyProjectStateChange(true)
	c.JSON(http.StatusOK, gin.H{"id": info.ID, "name": info.Name, "db_schema_version": store.DBSchemaVersion()})
}
func (h *ProjectHandler) selectDir(c *gin.Context) {
	if h.selector == nil {
		problem(c, 501, "INVALID_SELECTION", "Native directory selection is unavailable")
		return
	}
	token, expires, err := h.manager.IssueSelection(c.Request.Context(), h.selector)
	if err != nil {
		writeProjectError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"token": token, "expires_at": expires})
}
func (h *ProjectHandler) open(c *gin.Context) {
	var request struct {
		SelectionToken string `json:"selection_token"`
		Mode           string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		problem(c, 400, "INVALID_SELECTION", "Invalid project request")
		return
	}
	var info project.ProjectInfo
	var err error
	switch request.Mode {
	case "create":
		info, err = h.manager.Create(c.Request.Context(), request.SelectionToken)
	case "open":
		info, err = h.manager.Open(c.Request.Context(), request.SelectionToken)
	default:
		problem(c, 400, "INVALID_SELECTION", "Unsupported project mode")
		return
	}
	if err != nil {
		writeProjectError(c, err)
		return
	}
	status := http.StatusOK
	if request.Mode == "create" {
		status = http.StatusCreated
	}
	h.notifyProjectStateChange(true)
	c.JSON(status, gin.H{"id": info.ID, "name": info.Name, "db_schema_version": store.DBSchemaVersion()})
}
func (h *ProjectHandler) current(c *gin.Context) {
	info, ok := h.manager.Current()
	if !ok {
		problem(c, 404, "PROJECT_NOT_OPEN", "No active project")
		return
	}
	c.JSON(200, gin.H{"id": info.ID, "name": info.Name, "db_schema_version": store.DBSchemaVersion()})
}
func (h *ProjectHandler) close(c *gin.Context) {
	if err := h.manager.Close(c.Request.Context()); err != nil {
		writeProjectError(c, err)
		return
	}
	h.notifyProjectStateChange(false)
	c.Status(204)
}
func writeProjectError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, project.ErrInvalidSelection):
		problem(c, 400, "INVALID_SELECTION", "Invalid project selection")
	case errors.Is(err, project.ErrProjectLocked):
		problem(c, 423, "PROJECT_LOCKED", "Project is locked")
	case errors.Is(err, project.ErrActiveProject):
		problem(c, 409, "ACTIVE_PROJECT_CONFLICT", "Close the active project first")
	case errors.Is(err, project.ErrCloseBlocked):
		problem(c, 409, "CLOSE_BLOCKED", "Project close is blocked")
	case errors.Is(err, project.ErrLockOwnerUnknown):
		problem(c, 409, "OTHER_INSTANCE_UNAVAILABLE", "The lock owner cannot be safely identified")
	case errors.Is(err, project.ErrOtherUnavailable):
		problem(c, 503, "OTHER_INSTANCE_UNAVAILABLE", "The other instance is unavailable")
	case errors.Is(err, project.ErrOtherRejected):
		problem(c, 409, "OTHER_INSTANCE_UNAVAILABLE", "The other instance rejected the close request")
	default:
		problem(c, 500, "VALIDATION_FAILED", "Project operation failed")
	}
}
