package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/project"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

// simulationRunReader is deliberately read-only: historical simulation facts
// must never be materialized, replayed, or repaired by a GET request.
type simulationRunReader interface {
	GetSimulationRun(context.Context, domain.ID) (store.SimulationRun, []store.SimulationMetricResult, error)
	GetSimulationJobMaterialization(context.Context, domain.ID) (contract.JobMaterialization, error)
	ListSimulationVerifications(context.Context, domain.ID) ([]store.SimulationVerification, error)
}

type SimulationRunStoreProvider func() simulationRunReader
type SimulationRunProjector func(store.SimulationRun) contract.HistoricalRunProjection

type simulationJobReader interface {
	GetJob(context.Context, domain.ID) (sharedjob.Record, error)
	RequestCancellation(context.Context, domain.ID) (sharedjob.Record, bool, error)
	ListEvents(context.Context, domain.ID, int64) ([]sharedjob.Event, error)
}

type SimulationJobStoreProvider func() simulationJobReader

// SimulationRunStoreFromProjectManager exposes the active project's scoped
// immutable run reader. It does not open projects or construct another store.
func SimulationRunStoreFromProjectManager(manager *project.Manager) SimulationRunStoreProvider {
	return func() simulationRunReader {
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

// SimulationJobStoreFromProjectManager exposes the same project-scoped Store
// through the shared durable Job protocol.
func SimulationJobStoreFromProjectManager(manager *project.Manager) SimulationJobStoreProvider {
	return func() simulationJobReader {
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

// SimulationJobResolver lets simulation jobs use the existing GET, SSE, and
// cancellation endpoints without creating a parallel Job transport contract.
func SimulationJobResolver(provider SimulationJobStoreProvider) DurableResolverFunc {
	get := func(ctx context.Context, id domain.ID) (sharedjob.Record, bool) {
		if provider == nil || !id.Valid() {
			return sharedjob.Record{}, false
		}
		reader := provider()
		if reader == nil {
			return sharedjob.Record{}, false
		}
		job, err := reader.GetJob(ctx, id)
		return job, err == nil && job.Kind == "simulation"
	}
	return DurableResolverFunc{
		Get: func(ctx context.Context, id domain.ID) (map[string]any, bool, error) {
			job, found := get(ctx, id)
			if !found {
				return nil, false, nil
			}
			return simulationJobJSON(job), true, nil
		},
		Cancel: func(ctx context.Context, id domain.ID) (map[string]any, bool, bool, error) {
			job, found := get(ctx, id)
			if !found {
				return nil, false, false, nil
			}
			reader := provider()
			updated, replay, err := reader.RequestCancellation(ctx, job.ID)
			if err != nil || updated.Kind != "simulation" {
				return nil, false, false, err
			}
			return simulationJobJSON(updated), true, !replay, nil
		},
		Events: func(ctx context.Context, id domain.ID, after int64) ([]DurableJobEvent, error) {
			job, found := get(ctx, id)
			if !found {
				return nil, nil
			}
			events, err := provider().ListEvents(ctx, job.ID, after)
			if err != nil {
				return nil, err
			}
			result := make([]DurableJobEvent, 0, len(events))
			for _, event := range events {
				result = append(result, DurableJobEvent{Ordinal: event.Ordinal, Payload: simulationJobEventJSON(event)})
			}
			return result, nil
		},
	}
}

// SimulationHandler is the immutable simulation-run HTTP adapter.
type SimulationHandler struct {
	store   SimulationRunStoreProvider
	project SimulationRunProjector
}

func NewSimulationHandler(provider SimulationRunStoreProvider) *SimulationHandler {
	manifest, err := contract.NewManifestRegistry(contract.RequiredV1Descriptors, contract.V1Descriptors())
	if err != nil {
		return NewSimulationHandlerWithCompatibility(provider, nil)
	}
	registry, err := contract.NewImplementationRegistry(manifest)
	if err != nil {
		return NewSimulationHandlerWithCompatibility(provider, nil)
	}
	return NewSimulationHandlerWithCompatibility(provider, func(run store.SimulationRun) contract.HistoricalRunProjection {
		return registry.ProjectHistoricalRun(run.CanonicalResult, run.InputHash, run.FingerprintHash, run.ResultHash, run.Implementations)
	})
}

// NewSimulationHandlerWithCompatibility allows startup composition to supply
// the retained implementation registry selected for this process.
func NewSimulationHandlerWithCompatibility(provider SimulationRunStoreProvider, projector SimulationRunProjector) *SimulationHandler {
	if projector == nil {
		projector = func(run store.SimulationRun) contract.HistoricalRunProjection {
			return contract.HistoricalRunProjection{CanonicalResult: run.CanonicalResult, InputHash: run.InputHash, FingerprintHash: run.FingerprintHash, ResultHash: run.ResultHash, Reasons: []string{"simulation compatibility registry is unavailable"}}
		}
	}
	return &SimulationHandler{store: provider, project: projector}
}

func (h *SimulationHandler) Register(r *gin.Engine) {
	r.GET("/api/v1/simulation-runs/:id", h.getRun)
}

func (h *SimulationHandler) current(c *gin.Context) simulationRunReader {
	if h.store == nil {
		problem(c, http.StatusServiceUnavailable, "SIMULATION_CAPABILITY_UNAVAILABLE", "Simulation capability is unavailable")
		return nil
	}
	reader := h.store()
	if reader == nil {
		problem(c, http.StatusServiceUnavailable, "SIMULATION_CAPABILITY_UNAVAILABLE", "Simulation capability is unavailable")
	}
	return reader
}

func (h *SimulationHandler) getRun(c *gin.Context) {
	reader := h.current(c)
	if reader == nil {
		return
	}
	id, ok := idParam(c, "SIMULATION_RUN_NOT_FOUND")
	if !ok {
		return
	}
	run, metrics, err := reader.GetSimulationRun(c.Request.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		problem(c, http.StatusNotFound, "SIMULATION_RUN_NOT_FOUND", "Simulation run not found")
		return
	}
	if err != nil {
		problem(c, http.StatusServiceUnavailable, "SIMULATION_RUN_UNAVAILABLE", "Simulation run is unavailable")
		return
	}
	projection := h.project(run)
	var input *contract.SimulationInputV1
	materialization, materializationErr := reader.GetSimulationJobMaterialization(c.Request.Context(), run.JobID)
	if materializationErr == nil && materialization.JobID == run.JobID && materialization.ProjectID == run.ProjectID && materialization.RevisionID == run.RevisionID && materialization.ScenarioDefinitionID == run.ScenarioDefinitionID && materialization.InputHash == run.InputHash && materialization.FingerprintHash == run.FingerprintHash {
		captured, parseErr := contract.ParseCanonicalInput(materialization.CanonicalInput)
		if parseErr == nil && captured.ProjectID == contract.ID(run.ProjectID) && captured.RevisionID == contract.ID(run.RevisionID) {
			input = &captured
		}
	}
	if input == nil {
		projection.Reproducible = false
		projection.Reasons = append(projection.Reasons, "simulation input materialization is unavailable")
	}
	verifications, err := reader.ListSimulationVerifications(c.Request.Context(), id)
	if err != nil {
		problem(c, http.StatusServiceUnavailable, "SIMULATION_RUN_UNAVAILABLE", "Simulation run is unavailable")
		return
	}
	c.JSON(http.StatusOK, simulationRunJSON(run, metrics, verifications, projection, input))
}

func simulationRunJSON(run store.SimulationRun, metrics []store.SimulationMetricResult, verifications []store.SimulationVerification, projection contract.HistoricalRunProjection, input *contract.SimulationInputV1) gin.H {
	metricItems := make([]gin.H, 0, len(metrics))
	for _, metric := range metrics {
		metricItems = append(metricItems, gin.H{"id": metric.MetricID, "version": metric.MetricVersion, "status": metric.Status, "canonical_result": metric.CanonicalResult})
	}
	verificationItems := make([]gin.H, 0, len(verifications))
	for _, verification := range verifications {
		verificationItems = append(verificationItems, gin.H{"id": verification.ID, "source_run_id": verification.SourceRunID, "reproduction_run_id": verification.ReproductionRunID, "status": verification.Status, "input_hash": verification.InputHash, "fingerprint_hash": verification.FingerprintHash, "source_result_hash": verification.SourceResultHash, "reproduction_result_hash": verification.ReproductionResultHash, "created_at": verification.CreatedAt})
	}
	return gin.H{"id": run.ID, "job_id": run.JobID, "revision_id": run.RevisionID, "scenario_definition_id": run.ScenarioDefinitionID, "input": input, "input_hash": projection.InputHash, "fingerprint_hash": projection.FingerprintHash, "result_hash": projection.ResultHash, "canonical_result": projection.CanonicalResult, "metrics": metricItems, "verification_refs": verificationItems, "reproducible": projection.Reproducible, "reasons": projection.Reasons, "created_at": run.CreatedAt}
}

func simulationJobJSON(job sharedjob.Record) gin.H {
	response := gin.H{"id": job.ID, "kind": job.Kind, "revision_id": job.RevisionID, "status": job.Status, "input_hash": job.InputHash, "request_hash": job.RequestHash, "events_url": "/api/v1/jobs/" + string(job.ID) + "/events", "cancel_generation": job.CancelGeneration, "cancel_requested_at": job.CancelRequestedAt, "created_at": job.CreatedAt, "updated_at": job.UpdatedAt, "poll_after_ms": 1000}
	if job.Result != nil {
		response["result_type"] = job.Result.Type
		response["result_id"] = job.Result.ID
		response["result_url"] = job.Result.URL
	}
	return response
}

func simulationJobEventJSON(event sharedjob.Event) gin.H {
	response := gin.H{"job_id": event.JobID, "ordinal": event.Ordinal, "phase": event.Phase, "progress": event.Progress, "created_at": event.CreatedAt}
	if event.Warning != "" {
		response["warning"] = event.Warning
	}
	if event.SafeError != "" {
		response["error"] = event.SafeError
	}
	if event.Result != nil {
		response["result_type"] = event.Result.Type
		response["result_id"] = event.Result.ID
		response["result_url"] = event.Result.URL
	}
	return response
}
