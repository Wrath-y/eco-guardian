package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	appruntime "github.com/zouyi/eco-guardian/internal/app/runtime"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	"github.com/zouyi/eco-guardian/internal/backup/rootconfig"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
	"github.com/zouyi/eco-guardian/internal/project"
)

const maxSettingsRequestBytes = 16 << 10

type CredentialPresence func(provider string) bool

type SettingsHandler struct {
	application appruntime.SettingsApplication
	credential  CredentialPresence
	backupRoot  BackupRootSettings
}

type BackupRootSelection interface {
	IssueSelection(context.Context, project.DirectorySelector) (string, time.Time, error)
	ApplyNativeSelection(context.Context, string, string) error
	ResetDefault(context.Context) error
	Validate(context.Context) error
}

type BackupRootSettings struct {
	Selection BackupRootSelection
	Selector  project.DirectorySelector
	Projects  *project.Manager
}

func NewSettingsHandler(store appruntime.SettingsRepository, credential CredentialPresence, backupRoot ...BackupRootSettings) *SettingsHandler {
	if credential == nil {
		credential = func(string) bool { return false }
	}
	handler := &SettingsHandler{application: appruntime.SettingsApplication{Repository: store}, credential: credential}
	if len(backupRoot) > 0 {
		handler.backupRoot = backupRoot[0]
	}
	return handler
}

func (h *SettingsHandler) Register(router *gin.Engine) {
	router.GET("/api/v1/settings", h.get)
	router.PATCH("/api/v1/settings", h.patch)
	router.POST("/api/v1/settings/backup-root-selection", h.selectBackupRoot)
}

func (h *SettingsHandler) selectBackupRoot(c *gin.Context) {
	if !safeLoopbackOrigin(c.Request) {
		problem(c, http.StatusForbidden, "SETTINGS_INVALID", "Request origin is not allowed")
		return
	}
	if h.backupRoot.Selection == nil || h.backupRoot.Selector == nil {
		problem(c, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "Native backup root selection is unavailable")
		return
	}
	token, _, err := h.backupRoot.Selection.IssueSelection(c.Request.Context(), h.backupRoot.Selector)
	if err != nil {
		h.writeBackupRootError(c, err)
		return
	}
	activeDirectory := ""
	if h.backupRoot.Projects != nil {
		if current, ok := h.backupRoot.Projects.Current(); ok {
			activeDirectory = current.Path
		}
	}
	if err = h.backupRoot.Selection.ApplyNativeSelection(c.Request.Context(), token, activeDirectory); err != nil {
		h.writeBackupRootError(c, err)
		return
	}
	settings, readErr := h.application.Read(c.Request.Context())
	if readErr != nil {
		h.writeError(c, readErr)
		return
	}
	c.JSON(http.StatusOK, riskdto.SettingsUpdateResult{
		Settings: h.resource(c.Request.Context(), settings),
		Effects:  []riskdto.SettingsApplyEffect{{Field: "backup.root", Disposition: riskdto.Applied}},
	})
}

func (h *SettingsHandler) get(c *gin.Context) {
	settings, err := h.application.Read(c.Request.Context())
	if err != nil {
		problem(c, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "Settings are unavailable")
		return
	}
	c.JSON(http.StatusOK, h.resource(c.Request.Context(), settings))
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
		backup := runtimeconfig.BackupDefaults{}
		if request.Backup.DailyRetentionCount != nil {
			backup.DailyRetentionCount = *request.Backup.DailyRetentionCount
		}
		if request.Backup.ReleaseMigrationRetentionCount != nil {
			backup.ReleaseMigrationRetention = *request.Backup.ReleaseMigrationRetentionCount
		}
		if request.Backup.UseDefaultRoot != nil && bool(*request.Backup.UseDefaultRoot) {
			if h.backupRoot.Selection == nil {
				problem(c, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "Default backup root reset is unavailable")
				return
			}
			if err := h.backupRoot.Selection.ResetDefault(c.Request.Context()); err != nil {
				h.writeBackupRootError(c, err)
				return
			}
		}
		patch.Backup = &backup
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
	c.JSON(http.StatusOK, riskdto.SettingsUpdateResult{Settings: h.resource(c.Request.Context(), result.Settings), Effects: effects})
}

func (h *SettingsHandler) writeBackupRootError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, rootconfig.ErrSelectionInvalid), errors.Is(err, project.ErrInvalidSelection):
		problemDetails(c, http.StatusBadRequest, "SETTINGS_INVALID", "Backup root selection is invalid", nil, "backup.root")
	case errors.Is(err, rootconfig.ErrRootOverlap):
		problemDetails(c, http.StatusConflict, "BACKUP_PATH_SECURITY", "Backup root overlaps the active project", nil, "backup.root")
	case errors.Is(err, backupfs.ErrSpaceInsufficient):
		problemDetails(c, http.StatusInsufficientStorage, "BACKUP_SPACE_INSUFFICIENT", "Backup root has insufficient free space", nil, "backup.root")
	default:
		problemDetails(c, http.StatusServiceUnavailable, "BACKUP_ROOT_UNWRITABLE", "Backup root is unavailable or not writable", nil, "backup.root")
	}
}

func (h *SettingsHandler) resource(ctx context.Context, settings runtimeconfig.Settings) riskdto.SettingsResource {
	packageMode := riskdto.PackageSettingsMode(settings.Package.Mode)
	rootHealth := riskdto.BackupRootHealth("unknown")
	if h.backupRoot.Selection != nil {
		if err := h.backupRoot.Selection.Validate(ctx); err == nil {
			rootHealth = riskdto.BackupRootHealth("healthy")
		} else if errors.Is(err, backupfs.ErrSpaceInsufficient) {
			rootHealth = riskdto.BackupRootHealth("insufficient_space")
		} else if errors.Is(err, backupfs.ErrRootUnwritable) {
			rootHealth = riskdto.BackupRootHealth("unwritable")
		} else {
			rootHealth = riskdto.BackupRootHealth("unavailable")
		}
	}
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
		Logs: riskdto.LogSettings{MaxBytes: settings.Logs.MaxBytes, MaxFiles: settings.Logs.MaxFiles},
		Backup: riskdto.BackupDefaultSettings{
			RetentionDays: settings.Backup.RetentionDays, RootSelectionState: riskdto.BackupRootSelectionState(settings.Backup.RootMode),
			DailyRetentionCount: settings.Backup.DailyRetentionCount, ReleaseMigrationRetentionCount: settings.Backup.ReleaseMigrationRetention,
			RootHealth: rootHealth,
		},
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
