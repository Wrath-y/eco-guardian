package httpapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	appruntime "github.com/zouyi/eco-guardian/internal/app/runtime"
	"github.com/zouyi/eco-guardian/internal/app/runtime/capability"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
)

type RuntimeStatusSource interface {
	Snapshot() appruntime.StatusResource
}

type RuntimeStatusHandler struct{ source RuntimeStatusSource }

func NewRuntimeStatusHandler(source RuntimeStatusSource) *RuntimeStatusHandler {
	return &RuntimeStatusHandler{source: source}
}

func (handler *RuntimeStatusHandler) Register(engine *gin.Engine) {
	engine.GET("/api/v1/runtime/status", handler.get)
}

func (handler *RuntimeStatusHandler) get(context *gin.Context) {
	if handler == nil || handler.source == nil {
		problem(context, http.StatusServiceUnavailable, "RUNTIME_STATUS_UNAVAILABLE", "Runtime status is unavailable")
		return
	}
	snapshot := handler.source.Snapshot()
	if snapshot.SchemaVersion != 1 || snapshot.ListenerURL == "" || snapshot.UpdatedAt.IsZero() {
		problem(context, http.StatusServiceUnavailable, "RUNTIME_STATUS_UNAVAILABLE", "Runtime status is not ready")
		return
	}
	context.JSON(http.StatusOK, runtimeStatusDTO(snapshot))
}

func runtimeStatusDTO(snapshot appruntime.StatusResource) riskdto.RuntimeStatusResource {
	dependencies := make([]riskdto.RuntimeDependencyObservation, 0, len(snapshot.Dependencies))
	for _, observation := range snapshot.Dependencies {
		dependencies = append(dependencies, riskdto.RuntimeDependencyObservation{
			Id: observation.ID, State: riskdto.RuntimeDependencyObservationState(observation.State), Generation: int64(observation.Generation),
			ObservedAt: optionalTime(observation.ObservedAt), ExpiresAt: optionalTime(observation.ExpiresAt), Reasons: runtimeReasons(observation.Reasons),
		})
	}
	capabilities := make([]riskdto.RuntimeCapabilityResult, 0, len(snapshot.Capabilities))
	for _, result := range snapshot.Capabilities {
		actions := make([]riskdto.RuntimeAction, 0, len(result.Actions))
		for _, action := range result.Actions {
			actions = append(actions, riskdto.RuntimeAction{Id: riskdto.RuntimeActionId(action.ID), Method: riskdto.RuntimeActionMethod(action.Method), Uri: action.URI, IdempotencyRequired: action.IdempotencyRequired})
		}
		capabilities = append(capabilities, riskdto.RuntimeCapabilityResult{Id: result.ID, Version: result.Version, State: riskdto.RuntimeCapabilityResultState(result.State), ObservationGeneration: int64(result.ObservationGeneration), Reasons: runtimeReasons(result.Reasons), Actions: actions})
	}
	recovery := make([]riskdto.RuntimeRecoverySummary, 0, len(snapshot.Recovery))
	for _, summary := range snapshot.Recovery {
		recovery = append(recovery, riskdto.RuntimeRecoverySummary{JobKind: summary.JobKind, State: riskdto.RuntimeRecoverySummaryState(summary.State), Count: summary.Count})
	}
	var projectID *riskdto.UUIDv7
	if snapshot.Project.ProjectID != "" {
		if parsed, err := uuid.Parse(snapshot.Project.ProjectID); err == nil {
			projectID = &parsed
		}
	}
	return riskdto.RuntimeStatusResource{
		SchemaVersion: 1, Generation: int64(snapshot.Generation),
		Build:    riskdto.RuntimeBuildIdentity{Version: snapshot.Build.Version, Build: snapshot.Build.Build, Commit: snapshot.Build.Commit, PackageMode: riskdto.RuntimeBuildIdentityPackageMode(snapshot.Build.PackageMode)},
		Listener: riskdto.RuntimeListener{Url: snapshot.ListenerURL}, Phase: riskdto.RuntimeStatusResourcePhase(snapshot.Phase),
		Project:      riskdto.RuntimeProjectSummary{State: riskdto.RuntimeProjectSummaryState(snapshot.Project.State), ProjectId: projectID, RecentCount: snapshot.Project.RecentCount, RecoveryRequired: snapshot.Project.RecoveryRequired},
		Process:      riskdto.RuntimeProcessSummary{Ownership: riskdto.RuntimeProcessSummaryOwnership(snapshot.Process.Ownership), State: riskdto.RuntimeProcessSummaryState(snapshot.Process.State), LaunchGeneration: int64(snapshot.Process.Generation), Endpoint: optionalString(snapshot.Process.Endpoint), RestartAttempt: snapshot.Process.RestartAttempt, Reason: optionalString(snapshot.Process.Reason)},
		Dependencies: dependencies, Capabilities: capabilities, Recovery: recovery, LogLocation: snapshot.LogLocation, UpdatedAt: snapshot.UpdatedAt,
	}
}

func runtimeReasons(values []capability.Reason) []riskdto.RuntimeReason {
	result := make([]riskdto.RuntimeReason, 0, len(values))
	for _, value := range values {
		result = append(result, riskdto.RuntimeReason{Code: value.Code, Component: value.Component, ObservationGeneration: int64(value.ObservationGeneration)})
	}
	return result
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	value = value.UTC()
	return &value
}
