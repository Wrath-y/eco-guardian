package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	"github.com/zouyi/eco-guardian/internal/validation"
)

type ValidationStoreProvider func() *store.Store

func ValidationStoreFromProjectManager(manager *project.Manager) ValidationStoreProvider {
	return func() *store.Store {
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

type ValidationHandler struct{ store ValidationStoreProvider }

func NewValidationHandler(store ValidationStoreProvider) *ValidationHandler {
	return &ValidationHandler{store}
}
func (h *ValidationHandler) Register(engine *gin.Engine) {
	engine.POST("/api/v1/validation/runs", h.create)
	engine.GET("/api/v1/validation/runs/:id", h.get)
}

type validationRequest struct {
	Source struct {
		Type       string    `json:"type"`
		RevisionID domain.ID `json:"revision_id"`
	} `json:"source"`
	Scope     validation.Scope `json:"scope"`
	EntityIDs []domain.ID      `json:"entity_ids"`
}

func (h *ValidationHandler) current(c *gin.Context) *store.Store {
	s := h.store()
	if s == nil {
		problem(c, http.StatusNotFound, "PROJECT_NOT_OPEN", "No active project")
	}
	return s
}
func (h *ValidationHandler) create(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	var request validationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		problem(c, 400, "INVALID_VALIDATION_REQUEST", "Invalid validation request")
		return
	}
	var kind validation.SourceKind
	switch request.Source.Type {
	case "working":
		kind = validation.SourceWorking
		if request.Source.RevisionID != "" {
			problem(c, 400, "INVALID_VALIDATION_SOURCE", "Working source cannot include revision_id")
			return
		}
	case "revision":
		kind = validation.SourceRevision
		if !request.Source.RevisionID.Valid() {
			problem(c, 400, "INVALID_VALIDATION_SOURCE", "Revision source requires revision_id")
			return
		}
	default:
		problem(c, 400, "INVALID_VALIDATION_SOURCE", "Source type must be working or revision")
		return
	}
	if request.Scope != validation.ScopeLocal && request.Scope != validation.ScopeFull {
		problem(c, 400, "INVALID_VALIDATION_SCOPE", "Scope must be LOCAL or FULL")
		return
	}
	if request.Scope == validation.ScopeLocal && len(request.EntityIDs) == 0 {
		problem(c, 400, "INVALID_VALIDATION_TARGET", "LOCAL requires entity_ids")
		return
	}
	if request.Scope == validation.ScopeFull && len(request.EntityIDs) > 0 {
		problem(c, 400, "INVALID_VALIDATION_TARGET", "FULL forbids entity_ids")
		return
	}
	report, err := s.RunValidation(c.Request.Context(), kind, request.Source.RevisionID, request.Scope)
	if err != nil {
		if err == store.ErrNotFound {
			problem(c, 404, "VALIDATION_SOURCE_NOT_FOUND", "Validation source not found")
			return
		}
		problem(c, 500, "VALIDATION_STORAGE_FAILURE", "Validation storage failure")
		return
	}
	c.JSON(http.StatusCreated, validationResponse(report))
}
func (h *ValidationHandler) get(c *gin.Context) {
	s := h.current(c)
	if s == nil {
		return
	}
	id := domain.ID(c.Param("id"))
	if !id.Valid() {
		problem(c, 404, "VALIDATION_RUN_NOT_FOUND", "Validation run not found")
		return
	}
	report, err := s.GetValidationReport(c.Request.Context(), string(id))
	if err == store.ErrNotFound {
		problem(c, 404, "VALIDATION_RUN_NOT_FOUND", "Validation run not found")
		return
	}
	if err != nil {
		problem(c, 500, "VALIDATION_STORAGE_FAILURE", "Validation storage failure")
		return
	}
	c.JSON(http.StatusOK, validationResponse(report))
}
func validationResponse(report store.ValidationReport) gin.H {
	source := gin.H{"type": report.Run.Source.Kind}
	if report.Run.Source.RevisionID != "" {
		source["revision_id"] = report.Run.Source.RevisionID
	}
	issues := make([]gin.H, 0, len(report.Issues))
	for _, issue := range report.Issues {
		message, hint := validation.Presentation(issue)
		issues = append(issues, gin.H{"severity": issue.Severity, "code": issue.Code, "entity_id": issue.EntityID, "field_path": issue.FieldPath, "formula_span": issue.Span, "ordinal": issue.Ordinal, "message_key": issue.MessageKey, "message_params": issue.MessageParams, "fix_hint_key": issue.FixHintKey, "message": message, "fix_hint": hint, "evidence": issue.Evidence, "fingerprint": issue.Fingerprint})
	}
	return gin.H{"id": report.Run.ID, "source": source, "scope": report.Run.Scope, "input_hash": report.Run.Source.InputHash, "versions": report.Run.Versions, "status": report.Run.Status, "summary": report.Run.Summary, "result_hash": report.Run.ResultHash, "created_at": report.Run.CreatedAt, "issues": issues}
}
