package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
)

const maxSettingsRequestBytes = 16 << 10

type settingsStore interface {
	Load() (runtimeconfig.Settings, bool, error)
	Save(runtimeconfig.Settings) error
}

type CredentialPresence func(provider string) bool

type SettingsHandler struct {
	store      settingsStore
	credential CredentialPresence
}

func NewSettingsHandler(store settingsStore, credential CredentialPresence) *SettingsHandler {
	if credential == nil {
		credential = func(string) bool { return false }
	}
	return &SettingsHandler{store: store, credential: credential}
}

func (h *SettingsHandler) Register(router *gin.Engine) {
	router.GET("/api/v1/settings", h.get)
	router.PATCH("/api/v1/settings", h.patch)
}

func (h *SettingsHandler) get(c *gin.Context) {
	settings, _, err := h.store.Load()
	if err != nil {
		problem(c, http.StatusInternalServerError, "STORAGE_FAILURE", "Settings are unavailable")
		return
	}
	c.JSON(http.StatusOK, h.resource(settings))
}

func (h *SettingsHandler) patch(c *gin.Context) {
	if !safeLoopbackOrigin(c.Request) {
		problem(c, http.StatusForbidden, "AI_INPUT_INVALID", "Request origin is not allowed")
		return
	}
	var request riskdto.PatchSettingsRequest
	if !decodeStrictAIJSONLimit(c, &request, maxSettingsRequestBytes) {
		return
	}
	settings, _, err := h.store.Load()
	if err != nil {
		problem(c, http.StatusInternalServerError, "STORAGE_FAILURE", "Settings are unavailable")
		return
	}
	settings.AI = runtimeconfig.AIReference{
		Enabled:               request.Ai.Enabled,
		Endpoint:              request.Ai.Endpoint,
		Model:                 request.Ai.Model,
		RequestTimeoutSeconds: request.Ai.RequestTimeoutSeconds,
		AllowCloud:            request.Ai.AllowCloud,
	}
	if err := h.store.Save(settings); err != nil {
		problem(c, http.StatusBadRequest, "AI_INPUT_INVALID", "Settings request is invalid")
		return
	}
	c.JSON(http.StatusOK, h.resource(settings))
}

func (h *SettingsHandler) resource(settings runtimeconfig.Settings) gin.H {
	return gin.H{
		"schema_version": settings.SchemaVersion,
		"ai": gin.H{
			"enabled":                 settings.AI.Enabled,
			"endpoint":                settings.AI.Endpoint,
			"model":                   settings.AI.Model,
			"request_timeout_seconds": settings.AI.RequestTimeoutSeconds,
			"allow_cloud":             settings.AI.AllowCloud,
			"endpoint_classification": endpointClassification(settings.AI.Endpoint, settings.AI.AllowCloud),
			"credential_present":      h.credential("openai-compatible"),
		},
	}
}

func endpointClassification(value string, allowCloud bool) any {
	if value == "" {
		return nil
	}
	classification, err := aiprovider.ValidateEndpoint(value, allowCloud)
	if err != nil {
		return nil
	}
	return classification
}
