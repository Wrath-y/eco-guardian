package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/formula"
)

type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
	RequestID string `json:"request_id"`
	EntityID  string `json:"entity_id,omitempty"`
	FieldPath string `json:"field_path,omitempty"`
	Details   any    `json:"details,omitempty"`
}

func problemDetails(c *gin.Context, status int, code, title string, details any, fieldPath string) {
	c.Header("Content-Type", "application/problem+json")
	c.JSON(status, Problem{Type: "urn:eco:problem:" + code, Title: title, Status: status, Code: code, Retryable: false, RequestID: c.GetHeader("X-Request-ID"), FieldPath: fieldPath, Details: details})
}

func problem(c *gin.Context, status int, code, title string) {
	c.Header("Content-Type", "application/problem+json")
	c.JSON(status, Problem{Type: "urn:eco:problem:" + code, Title: title, Status: status, Code: code, Retryable: false, RequestID: c.GetHeader("X-Request-ID")})
}

type SchemaHandler struct{ registry *domain.Registry }

func NewSchemaHandler(registry *domain.Registry) *SchemaHandler {
	return &SchemaHandler{registry: registry}
}
func (h *SchemaHandler) Register(engine *gin.Engine) {
	engine.GET("/api/v1/schemas/entities/:kind", h.get)
}
func (h *SchemaHandler) get(c *gin.Context) {
	kind := domain.EntityKind(c.Param("kind"))
	schema, ok := h.registry.Schema(kind)
	if !ok {
		problem(c, http.StatusBadRequest, "UNSUPPORTED_KIND", "Unsupported entity kind")
		return
	}
	dsl, err := formula.V1Registry()
	if err != nil {
		problem(c, http.StatusServiceUnavailable, "VALIDATION_STORAGE_FAILURE", "Formula registry unavailable")
		return
	}
	c.JSON(http.StatusOK, gin.H{"kind": kind, "schema_id": schema.ID, "schema": jsonRaw(schema.Raw), "ui_hints": gin.H{}, "dsl_registry": gin.H{"dsl_version": formula.DSLVersion, "manifest_hash": dsl.ManifestHash(), "selector_template": "${scope:symbol}", "scopes": []string{"self", "source", "target", "scenario"}, "units": dsl.Units(), "functions": dsl.Functions()}})
}

type jsonRaw []byte

func (j jsonRaw) MarshalJSON() ([]byte, error) { return j, nil }
