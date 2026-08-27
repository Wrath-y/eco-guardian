package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type BackupServiceProvider func() *application.Service
type DailyAdmissionProvider func() *application.DailyAdmission

type BackupHandler struct {
	service   BackupServiceProvider
	retention func() (int, int)
	daily     DailyAdmissionProvider
}

func NewBackupHandler(service BackupServiceProvider, retention func() (int, int), daily ...DailyAdmissionProvider) *BackupHandler {
	handler := &BackupHandler{service: service, retention: retention}
	if len(daily) > 0 {
		handler.daily = daily[0]
	}
	return handler
}

func (handler *BackupHandler) Register(engine *gin.Engine) {
	engine.GET("/api/v1/backups", handler.list)
	engine.POST("/api/v1/backups", handler.create)
	engine.GET("/api/v1/backups/:backup_id", handler.get)
	engine.POST("/api/v1/backups/daily-waivers", handler.confirmDailyWaiver)
	engine.POST("/api/v1/backups/daily-retries", handler.retryDailyBackup)
}

func (handler *BackupHandler) retryDailyBackup(c *gin.Context) {
	if !safeLoopbackOrigin(c.Request) {
		problem(c, http.StatusForbidden, "DAILY_WAIVER_INVALID", "Request origin is not allowed")
		return
	}
	if handler.daily == nil || handler.daily() == nil {
		problem(c, http.StatusServiceUnavailable, "BACKUP_UNAVAILABLE", "Daily backup admission is unavailable")
		return
	}
	var request struct {
		FailedJobID domain.ID `json:"failed_backup_job_id"`
	}
	if !decodeStrictAIJSONLimit(c, &request, 4096) {
		return
	}
	job, err := handler.daily().RetryFailed(c.Request.Context(), request.FailedJobID)
	if err != nil {
		var required application.DailyRequiredError
		if errors.As(err, &required) {
			problemDetails(c, http.StatusConflict, "DAILY_BACKUP_REQUIRED", "Daily backup retry failed; the edit remains uncommitted", gin.H{"failed_backup_job_id": required.JobID, "state": required.State}, "")
			return
		}
		problem(c, http.StatusConflict, "DAILY_WAIVER_INVALID", "Daily backup retry does not match the current failed backup")
		return
	}
	c.JSON(http.StatusOK, riskJobDTO(job, 0))
}

func (handler *BackupHandler) confirmDailyWaiver(c *gin.Context) {
	if !safeLoopbackOrigin(c.Request) {
		problem(c, http.StatusForbidden, "DAILY_WAIVER_INVALID", "Request origin is not allowed")
		return
	}
	if handler.daily == nil || handler.daily() == nil {
		problem(c, http.StatusServiceUnavailable, "BACKUP_UNAVAILABLE", "Daily backup admission is unavailable")
		return
	}
	var request struct {
		FailedJobID  domain.ID `json:"failed_backup_job_id"`
		Confirmation string    `json:"confirmation"`
	}
	if !decodeStrictAIJSONLimit(c, &request, 4096) {
		return
	}
	if !request.FailedJobID.Valid() || request.Confirmation != "CONTINUE_WITHOUT_BACKUP_TODAY" {
		problem(c, http.StatusBadRequest, "DAILY_WAIVER_INVALID", "Explicit daily backup waiver confirmation is required")
		return
	}
	waiver, replay, err := handler.daily().ConfirmWaiver(c.Request.Context(), request.FailedJobID, "local-user")
	if err != nil {
		problem(c, http.StatusConflict, "DAILY_WAIVER_INVALID", "Daily backup waiver does not match the current failed backup")
		return
	}
	c.JSON(http.StatusOK, gin.H{"project_uuid": waiver.ProjectID, "local_date": waiver.LocalDate, "failed_backup_job_id": waiver.FailedJobID, "confirmed_at": waiver.ConfirmedAt, "replay": replay})
}

func (handler *BackupHandler) current(c *gin.Context) *application.Service {
	if handler == nil || handler.service == nil {
		problem(c, http.StatusServiceUnavailable, "BACKUP_UNAVAILABLE", "Backup is unavailable")
		return nil
	}
	service := handler.service()
	if service == nil || !service.Valid() {
		problem(c, http.StatusServiceUnavailable, "BACKUP_UNAVAILABLE", "Backup is unavailable")
		return nil
	}
	return service
}

func (handler *BackupHandler) list(c *gin.Context) {
	service := handler.current(c)
	if service == nil {
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 || limit > 200 {
		problem(c, http.StatusBadRequest, "VALIDATION_FAILED", "limit must be between 1 and 200")
		return
	}
	var page ports.InventoryPage
	if service.Source == nil {
		page, err = service.ListInventory(c.Request.Context(), c.Query("cursor"), limit)
	} else {
		identity, identityErr := service.Source.Identity(c.Request.Context())
		if identityErr != nil {
			backupProblem(c, identityErr)
			return
		}
		page, err = service.Artifacts.List(c.Request.Context(), ports.InventoryQuery{ProjectID: identity.ProjectID, After: c.Query("cursor"), Limit: limit})
	}
	if err != nil {
		backupProblem(c, err)
		return
	}
	daily, mandatory := 10, 5
	if handler.retention != nil {
		daily, mandatory = handler.retention()
	}
	items := make([]gin.H, len(page.Items))
	for index, record := range page.Items {
		items[index] = backupRecordJSON(record)
	}
	body := gin.H{"items": items, "retention": gin.H{"daily_count": daily, "release_migration_count": mandatory, "manual_automatic_prune": false, "restore_pre_automatic_prune": false}}
	if page.NextCursor != "" {
		body["next_cursor"] = page.NextCursor
	}
	c.JSON(http.StatusOK, body)
}

func (handler *BackupHandler) get(c *gin.Context) {
	service := handler.current(c)
	if service == nil {
		return
	}
	backupID := domain.ID(c.Param("backup_id"))
	if !backupID.Valid() {
		problem(c, http.StatusNotFound, "BACKUP_NOT_FOUND", "Backup was not found")
		return
	}
	if service.Source == nil {
		page, listErr := service.ListInventory(c.Request.Context(), "", 200)
		if listErr != nil {
			backupProblem(c, listErr)
			return
		}
		for _, record := range page.Items {
			if record.BackupID == backupID {
				c.JSON(http.StatusOK, backupRecordJSON(record))
				return
			}
		}
		problem(c, http.StatusNotFound, "BACKUP_NOT_FOUND", "Backup was not found")
		return
	}
	identity, err := service.Source.Identity(c.Request.Context())
	if err != nil {
		backupProblem(c, err)
		return
	}
	cursor := ""
	for {
		page, listErr := service.Artifacts.List(c.Request.Context(), ports.InventoryQuery{ProjectID: identity.ProjectID, After: cursor, Limit: 200})
		if listErr != nil {
			backupProblem(c, listErr)
			return
		}
		for _, record := range page.Items {
			if record.BackupID == backupID {
				c.JSON(http.StatusOK, backupRecordJSON(record))
				return
			}
		}
		if page.NextCursor == "" {
			problem(c, http.StatusNotFound, "BACKUP_NOT_FOUND", "Backup was not found")
			return
		}
		cursor = page.NextCursor
	}
}

func (handler *BackupHandler) create(c *gin.Context) {
	service := handler.current(c)
	if service == nil {
		return
	}
	var request struct {
		Purpose string  `json:"purpose"`
		Reason  *string `json:"reason,omitempty"`
	}
	if !decodeStrictAIJSONLimit(c, &request, 4096) {
		return
	}
	if request.Purpose != string(backupdomain.Manual) {
		problem(c, http.StatusUnprocessableEntity, "BACKUP_PURPOSE_INVALID", "Only manual backup can be submitted here")
		return
	}
	if service.Source == nil {
		problem(c, http.StatusServiceUnavailable, "BACKUP_PROJECT_UNAVAILABLE", "An active project is required to create a backup")
		return
	}
	reason := "manual backup"
	if request.Reason != nil {
		reason = strings.TrimSpace(*request.Reason)
	}
	identity, err := service.Source.Identity(c.Request.Context())
	if err != nil {
		backupProblem(c, err)
		return
	}
	command := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: identity.ProjectID, Purpose: backupdomain.Manual, ManualReason: reason, Source: backupdomain.SourceIdentity{RevisionID: identity.RevisionID}}
	key := c.GetHeader("Idempotency-Key")
	job, _, err := service.Submit(c.Request.Context(), command, key)
	if err != nil {
		backupProblem(c, err)
		return
	}
	if !job.Status.Terminal() {
		workContext := context.WithoutCancel(c.Request.Context())
		go func() { _, _ = service.ExecuteStored(workContext, job.ID) }()
	}
	location := "/api/v1/jobs/" + string(job.ID)
	c.Header("Location", location)
	body := gin.H{"job": riskJobDTO(job, 0), "location": location, "result_type": "backup", "result_url": ""}
	if job.Result != nil {
		body["result_id"], body["result_url"] = job.Result.ID, job.Result.URL
	}
	c.JSON(http.StatusAccepted, body)
}

func BackupJobResolver(provider BackupServiceProvider) DurableResolverFunc {
	return DurableResolverFunc{
		Get: func(ctx context.Context, id domain.ID) (map[string]any, bool, error) {
			service := provider()
			if service == nil {
				return nil, false, nil
			}
			job, err := service.Jobs.GetJob(ctx, id)
			if errors.Is(err, store.ErrJobNotFound) || err == nil && job.Kind != application.JobKind {
				return nil, false, nil
			}
			return riskJobMap(job, latestBackupOrdinal(ctx, service, id)), true, err
		},
		Cancel: func(ctx context.Context, id domain.ID) (map[string]any, bool, bool, error) {
			service := provider()
			if service == nil {
				return nil, false, false, nil
			}
			job, err := service.Jobs.GetJob(ctx, id)
			if errors.Is(err, store.ErrJobNotFound) || err == nil && job.Kind != application.JobKind {
				return nil, false, false, nil
			}
			if err != nil {
				return nil, true, false, err
			}
			job, replay, err := service.Jobs.RequestCancellation(ctx, id)
			return riskJobMap(job, latestBackupOrdinal(ctx, service, id)), true, replay, err
		},
		Events: func(ctx context.Context, id domain.ID, after int64) ([]DurableJobEvent, error) {
			service := provider()
			if service == nil {
				return nil, nil
			}
			events, err := service.Events.ListEvents(ctx, id, after)
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

func latestBackupOrdinal(ctx context.Context, service *application.Service, id domain.ID) int64 {
	events, err := service.Events.ListEvents(ctx, id, 0)
	if err != nil || len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Ordinal
}

func backupRecordJSON(record backupdomain.InventoryRecord) gin.H {
	source := gin.H{}
	if record.Source.RevisionID.Valid() {
		source["revision_id"] = record.Source.RevisionID
	}
	if record.Source.ReleaseID.Valid() {
		source["release_id"] = record.Source.ReleaseID
	}
	if record.Source.MigrationID != "" {
		source["migration_id"] = record.Source.MigrationID
	}
	if record.Source.CallerJobID.Valid() {
		source["caller_job_id"] = record.Source.CallerJobID
	}
	if record.Source.RequestHash != "" {
		source["request_hash"] = record.Source.RequestHash
	}
	appVersion, schemaVersion, databaseBytes, databaseHash := any(record.AppVersion), any(record.SchemaVersion), any(record.DBBytes), any(record.DBSHA256)
	if record.AppVersion == "" {
		appVersion, schemaVersion, databaseBytes, databaseHash = nil, nil, nil, nil
	}
	return gin.H{"backup_id": record.BackupID, "project_uuid": record.ProjectID, "type": record.Type, "created_at": record.CreatedAt, "app_version": appVersion, "schema_version": schemaVersion, "db_bytes": databaseBytes, "db_sha256": databaseHash, "manifest_hash": record.ManifestHash, "validation_state": record.Validation, "compatibility_state": record.Compatibility, "source": source, "retention": record.Retention, "links": gin.H{"self": record.ResultURL}}
}

func backupProblem(c *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrFeatureDisabled):
		problem(c, http.StatusServiceUnavailable, "BACKUP_FEATURE_DISABLED", "Backup commands are disabled during feature rollback")
	case errors.Is(err, store.ErrJobIdempotencyConflict), errors.Is(err, backupdomain.ErrIdempotencyConflict):
		problem(c, http.StatusConflict, "BACKUP_IDEMPOTENCY_CONFLICT", "Idempotency key conflicts with a different backup request")
	case errors.Is(err, backupfs.ErrArtifactNotFound):
		problem(c, http.StatusNotFound, "BACKUP_NOT_FOUND", "Backup was not found")
	case errors.Is(err, backupfs.ErrArtifactDamaged):
		problem(c, http.StatusUnprocessableEntity, "BACKUP_DAMAGED", "Backup artifact failed validation")
	case errors.Is(err, application.ErrUnavailable):
		problem(c, http.StatusServiceUnavailable, "BACKUP_UNAVAILABLE", "Backup is unavailable")
	default:
		problem(c, http.StatusServiceUnavailable, "BACKUP_FAILED", "Backup operation failed")
	}
}
