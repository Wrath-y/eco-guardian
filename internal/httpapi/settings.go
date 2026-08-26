package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	appruntime "github.com/zouyi/eco-guardian/internal/app/runtime"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
)

const maxSettingsRequestBytes = 16 << 10

type CredentialPresence func(provider string) bool

type SettingsHandler struct {
	application appruntime.SettingsApplication
	credential  CredentialPresence
}

func NewSettingsHandler(store appruntime.SettingsRepository, credential CredentialPresence) *SettingsHandler {
	if credential == nil {
		credential = func(string) bool { return false }
	}
	return &SettingsHandler{application: appruntime.SettingsApplication{Repository: store}, credential: credential}
}

func (h *SettingsHandler) Register(router *gin.Engine) {
	router.GET("/api/v1/settings", h.get)
	router.PATCH("/api/v1/settings", h.patch)
}

func (h *SettingsHandler) get(c *gin.Context) {
	settings, err := h.application.Read(c.Request.Context())
	if err != nil {
		problem(c, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "Settings are unavailable")
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
	patch := appruntime.SettingsPatch{}
	if request.Browser != nil {
		patch.Browser = &runtimeconfig.Browser{AutoOpen: request.Browser.AutoOpen}
	}
	if request.Graph != nil {
		patch.Graph = &appruntime.GraphSettingsPatch{
			Mode: runtimeconfig.GraphMode(request.Graph.Mode), Endpoint: request.Graph.Endpoint,
			HealthTimeoutSeconds: request.Graph.HealthTimeoutSeconds, StartupTimeoutSeconds: request.Graph.StartupTimeoutSeconds,
			RestartLimit: request.Graph.RestartLimit,
		}
	}
	if request.Ai != nil {
		patch.AI = &runtimeconfig.AIReference{
			Enabled: request.Ai.Enabled, Endpoint: request.Ai.Endpoint, Model: request.Ai.Model,
			RequestTimeoutSeconds: request.Ai.RequestTimeoutSeconds, AllowCloud: request.Ai.AllowCloud,
		}
	}
	if request.Logs != nil {
		patch.Logs = &runtimeconfig.LogPolicy{MaxBytes: request.Logs.MaxBytes, MaxFiles: request.Logs.MaxFiles}
	}
	if request.Backup != nil {
		patch.Backup = &runtimeconfig.BackupDefaults{RetentionDays: request.Backup.RetentionDays}
	}
	result, err := h.application.Update(c.Request.Context(), patch)
	if err != nil {
		h.writeError(c, err)
		return
	}
	effects := make([]riskdto.SettingsApplyEffect, len(result.Effects))
	for index, effect := range result.Effects {
		effects[index] = riskdto.SettingsApplyEffect{Field: effect.Field, Disposition: riskdto.SettingsApplyDisposition(effect.Disposition)}
	}
	c.JSON(http.StatusOK, riskdto.SettingsUpdateResult{Settings: h.resource(result.Settings), Effects: effects})
}

func (h *SettingsHandler) resource(settings runtimeconfig.Settings) riskdto.SettingsResource {
	packageMode := riskdto.PackageSettingsMode(settings.Package.Mode)
	return riskdto.SettingsResource{
		SchemaVersion: settings.SchemaVersion,
		Browser:       riskdto.BrowserSettings{AutoOpen: settings.Browser.AutoOpen},
		Package:       riskdto.PackageSettings{Mode: &packageMode},
		Graph: riskdto.GraphSettings{
			Mode: riskdto.GraphSettingsMode(settings.Graph.Mode), Endpoint: settings.Graph.Endpoint,
			HealthTimeoutSeconds: settings.Graph.HealthTimeoutSeconds, StartupTimeoutSeconds: settings.Graph.StartupTimeoutSeconds,
			RestartLimit: settings.Graph.RestartLimit,
		},
		Ai: riskdto.AIProviderSettings{
			Enabled: settings.AI.Enabled, Endpoint: settings.AI.Endpoint, Model: settings.AI.Model,
			RequestTimeoutSeconds: settings.AI.RequestTimeoutSeconds, AllowCloud: settings.AI.AllowCloud,
			EndpointClassification: endpointClassification(settings.AI.Endpoint, settings.AI.AllowCloud),
			CredentialPresent:      h.credential("openai-compatible"),
		},
		Logs:   riskdto.LogSettings{MaxBytes: settings.Logs.MaxBytes, MaxFiles: settings.Logs.MaxFiles},
		Backup: riskdto.BackupDefaultSettings{RetentionDays: settings.Backup.RetentionDays},
	}
}

func (h *SettingsHandler) writeError(c *gin.Context, err error) {
	var validation runtimeconfig.ValidationError
	switch {
	case errors.As(err, &validation):
		problemDetails(c, http.StatusBadRequest, "SETTINGS_INVALID", "Settings request is invalid", gin.H{"reason": validation.Message}, validation.Field)
	case errors.Is(err, appruntime.ErrSettingsPatchEmpty):
		problem(c, http.StatusBadRequest, "SETTINGS_INVALID", "Settings request is empty")
	case errors.Is(err, appruntime.ErrSettingsUnavailable):
		problem(c, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "Settings are unavailable")
	default:
		problem(c, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "Settings could not be updated")
	}
}

func endpointClassification(value string, allowCloud bool) *riskdto.AIEndpointClassification {
	if value == "" {
		return nil
	}
	classification, err := aiprovider.ValidateEndpoint(value, allowCloud)
	if err != nil {
		return nil
	}
	result := riskdto.AIEndpointClassification(classification)
	return &result
}
