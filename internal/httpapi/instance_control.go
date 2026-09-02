package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
)

// InstanceControlHandler is a private server-to-server loopback control. The
// browser never receives the per-process token stored in the project lock.
type InstanceControlHandler struct {
	manager  instanceProjectManager
	secret   string
	shutdown func()
}

type instanceProjectManager interface {
	Current() (project.ProjectInfo, bool)
	Close(context.Context) error
}

func NewInstanceControlHandler(manager instanceProjectManager, secret string, shutdown func()) *InstanceControlHandler {
	return &InstanceControlHandler{manager: manager, secret: secret, shutdown: shutdown}
}

func (handler *InstanceControlHandler) Register(engine *gin.Engine) {
	engine.POST("/api/v1/internal/instances/close", handler.close)
}

func (handler *InstanceControlHandler) close(c *gin.Context) {
	provided := c.GetHeader(project.InstanceTokenHeader)
	if handler == nil || handler.manager == nil || handler.secret == "" || len(provided) != len(handler.secret) || subtle.ConstantTimeCompare([]byte(provided), []byte(handler.secret)) != 1 {
		problem(c, http.StatusUnauthorized, "OTHER_INSTANCE_UNAVAILABLE", "Instance control was rejected")
		return
	}
	var request struct {
		ProjectID domain.ID `json:"project_id"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || !request.ProjectID.Valid() {
		problem(c, http.StatusBadRequest, "INVALID_SELECTION", "Invalid project instance request")
		return
	}
	current, active := handler.manager.Current()
	if !active || current.ID != request.ProjectID {
		problem(c, http.StatusConflict, "OTHER_INSTANCE_UNAVAILABLE", "The instance no longer owns this project")
		return
	}
	if err := handler.manager.Close(c.Request.Context()); err != nil {
		writeProjectError(c, err)
		return
	}
	c.Status(http.StatusAccepted)
	if handler.shutdown != nil {
		go handler.shutdown()
	}
}
