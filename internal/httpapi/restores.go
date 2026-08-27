package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type RestoreServiceProvider func() *application.RestoreService

type RestoreHandler struct{ service RestoreServiceProvider }

func NewRestoreHandler(service RestoreServiceProvider) *RestoreHandler {
	return &RestoreHandler{service: service}
}

func (handler *RestoreHandler) Register(engine *gin.Engine) {
	engine.POST("/api/v1/restore-preflights", handler.preflight)
	engine.POST("/api/v1/restores", handler.create)
}

func (handler *RestoreHandler) current(c *gin.Context) *application.RestoreService {
	if handler == nil || handler.service == nil || handler.service() == nil || !handler.service().Valid() {
		problem(c, http.StatusServiceUnavailable, "RESTORE_UNAVAILABLE", "Restore is unavailable")
		return nil
	}
	return handler.service()
}

func (handler *RestoreHandler) preflight(c *gin.Context) {
	service := handler.current(c)
	if service == nil {
		return
	}
	var request struct {
		BackupID                  domain.ID `json:"backup_id"`
		TargetMode                string    `json:"target_mode"`
		SelectionToken            *string   `json:"selection_token,omitempty"`
		RegistryConfirmationToken *string   `json:"registry_confirmation_token,omitempty"`
	}
	if !decodeStrictAIJSONLimit(c, &request, 8192) {
		return
	}
	mode := backupdomain.RestoreTargetMode(request.TargetMode)
	selectionToken, registryToken := "", ""
	if request.SelectionToken != nil {
		selectionToken = *request.SelectionToken
	}
	if request.RegistryConfirmationToken != nil {
		registryToken = *request.RegistryConfirmationToken
	}
	activeInput := mode == backupdomain.RestoreActive && selectionToken == "" && registryToken == ""
	emptyInput := mode == backupdomain.RestoreEmptySelection && (selectionToken == "") != (registryToken == "")
	if !request.BackupID.Valid() || !activeInput && !emptyInput {
		problem(c, http.StatusBadRequest, "RESTORE_TARGET_INVALID", "Restore target input is invalid")
		return
	}
	preflight, err := service.PreflightTarget(c.Request.Context(), request.BackupID, mode, selectionToken, registryToken)
	if err != nil {
		restoreProblem(c, err)
		return
	}
	body := gin.H{"version": preflight.Version, "generation": preflight.Generation, "backup": backupRecordJSON(preflight.Backup), "target_mode": preflight.TargetMode, "writable": preflight.Writable, "free_space_sufficient": preflight.FreeSpaceSufficient, "maintenance_available": preflight.MaintenanceAvailable, "registry_state": preflight.RegistryState, "confirmation": preflight.Confirmation}
	if preflight.RegistryConfirmationToken != "" {
		body["registry_confirmation_token"] = preflight.RegistryConfirmationToken
	}
	c.JSON(http.StatusOK, body)
}

func (handler *RestoreHandler) create(c *gin.Context) {
	service := handler.current(c)
	if service == nil {
		return
	}
	var request struct {
		BackupID                  domain.ID `json:"backup_id"`
		TargetMode                string    `json:"target_mode"`
		PreflightGeneration       string    `json:"preflight_generation"`
		Confirmation              string    `json:"confirmation"`
		SelectionToken            *string   `json:"selection_token,omitempty"`
		RegistryConfirmationToken *string   `json:"registry_confirmation_token,omitempty"`
	}
	if !decodeStrictAIJSONLimit(c, &request, 8192) {
		return
	}
	mode := backupdomain.RestoreTargetMode(request.TargetMode)
	if request.SelectionToken != nil || request.RegistryConfirmationToken != nil {
		problem(c, http.StatusBadRequest, "RESTORE_TARGET_INVALID", "Restore target input is invalid")
		return
	}
	job, _, err := service.Submit(c.Request.Context(), request.PreflightGeneration, request.BackupID, mode, request.Confirmation, c.GetHeader("Idempotency-Key"))
	if err != nil {
		restoreProblem(c, err)
		return
	}
	if !job.Status.Terminal() {
		workContext := context.WithoutCancel(c.Request.Context())
		go func() { _, _ = service.Execute(workContext, job.ID) }()
	}
	location := "/api/v1/jobs/" + string(job.ID)
	c.Header("Location", location)
	body := gin.H{"job": riskJobDTO(job, 0), "location": location, "result_type": "restore", "result_url": ""}
	if job.Result != nil {
		body["result_id"], body["result_url"] = job.Result.ID, job.Result.URL
	}
	c.JSON(http.StatusAccepted, body)
}

func RestoreJobResolver(provider RestoreServiceProvider) DurableResolverFunc {
	return DurableResolverFunc{
		Get: func(ctx context.Context, id domain.ID) (map[string]any, bool, error) {
			service := provider()
			if service == nil {
				return nil, false, nil
			}
			job, err := service.GetJob(ctx, id)
			if errors.Is(err, store.ErrJobNotFound) || err == nil && job.Kind != application.RestoreJobKind {
				return nil, false, nil
			}
			return riskJobMap(job, latestRestoreOrdinal(ctx, service, id)), true, err
		},
		Cancel: func(ctx context.Context, id domain.ID) (map[string]any, bool, bool, error) {
			service := provider()
			if service == nil {
				return nil, false, false, nil
			}
			job, err := service.GetJob(ctx, id)
			if errors.Is(err, store.ErrJobNotFound) || err == nil && job.Kind != application.RestoreJobKind {
				return nil, false, false, nil
			}
			if err != nil {
				return nil, true, false, err
			}
			job, denied, err := service.Cancel(ctx, id)
			return riskJobMap(job, latestRestoreOrdinal(ctx, service, id)), true, denied, err
		},
		Events: func(ctx context.Context, id domain.ID, after int64) ([]DurableJobEvent, error) {
			service := provider()
			if service == nil {
				return nil, nil
			}
			events, err := service.ListEvents(ctx, id, after)
			if err != nil {
				return nil, err
			}
			result := make([]DurableJobEvent, 0, len(events))
			for _, event := range events {
				result = append(result, DurableJobEvent{Ordinal: event.Ordinal, Payload: riskJobEventJSON(event)})
			}
			return result, nil
		},
	}
}

func latestRestoreOrdinal(ctx context.Context, service *application.RestoreService, id domain.ID) int64 {
	events, err := service.ListEvents(ctx, id, 0)
	if err != nil || len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Ordinal
}

func restoreProblem(c *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrFeatureDisabled):
		problem(c, http.StatusServiceUnavailable, "RESTORE_FEATURE_DISABLED", "Restore commands are disabled during feature rollback")
	case errors.Is(err, application.ErrRestorePreflightStale):
		problem(c, http.StatusConflict, "RESTORE_PREFLIGHT_STALE", "Restore preflight must be repeated")
	case errors.Is(err, application.ErrRestoreIncompatible):
		problem(c, http.StatusUnprocessableEntity, "RESTORE_SCHEMA_NEWER", "Backup schema is newer than this application")
	case errors.Is(err, application.ErrRestoreIdentity):
		problem(c, http.StatusUnprocessableEntity, "RESTORE_UUID_MISMATCH", "Backup project identity does not match the active project")
	case errors.Is(err, application.ErrRestoreRecoveryRequired):
		problem(c, http.StatusServiceUnavailable, "RESTORE_RECOVERY_REQUIRED", "Restore recovery must complete before project use")
	case errors.Is(err, backupfs.ErrArtifactDamaged):
		problem(c, http.StatusUnprocessableEntity, "BACKUP_DAMAGED", "Backup artifact failed validation")
	case errors.Is(err, store.ErrJobIdempotencyConflict):
		problem(c, http.StatusConflict, "RESTORE_IDEMPOTENCY_CONFLICT", "Idempotency key conflicts with a different restore request")
	case errors.Is(err, application.ErrRestoreIdempotencyConflict):
		problem(c, http.StatusConflict, "RESTORE_IDEMPOTENCY_CONFLICT", "Idempotency key conflicts with a different restore request")
	case errors.Is(err, application.ErrRestoreUnavailable):
		problem(c, http.StatusServiceUnavailable, "RESTORE_UNAVAILABLE", "Restore is unavailable")
	default:
		problem(c, http.StatusServiceUnavailable, "RESTORE_FAILED", "Restore operation failed")
	}
}
