package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

// simulationRunReader is deliberately read-only: historical simulation facts
// must never be materialized, replayed, or repaired by a GET request.
type simulationRunReader interface {
	GetSimulationRun(context.Context, domain.ID) (store.SimulationRun, []store.SimulationMetricResult, error)
	ListSimulationVerifications(context.Context, domain.ID) ([]store.SimulationVerification, error)
}

type SimulationRunStoreProvider func() simulationRunReader

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

// SimulationHandler is the immutable simulation-run HTTP adapter.
type SimulationHandler struct{ store SimulationRunStoreProvider }

func NewSimulationHandler(store SimulationRunStoreProvider) *SimulationHandler {
	return &SimulationHandler{store: store}
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
	verifications, err := reader.ListSimulationVerifications(c.Request.Context(), id)
	if err != nil {
		problem(c, http.StatusServiceUnavailable, "SIMULATION_RUN_UNAVAILABLE", "Simulation run is unavailable")
		return
	}
	c.JSON(http.StatusOK, simulationRunJSON(run, metrics, verifications))
}

func simulationRunJSON(run store.SimulationRun, metrics []store.SimulationMetricResult, verifications []store.SimulationVerification) gin.H {
	metricItems := make([]gin.H, 0, len(metrics))
	for _, metric := range metrics {
		metricItems = append(metricItems, gin.H{"id": metric.MetricID, "version": metric.MetricVersion, "status": metric.Status, "canonical_result": metric.CanonicalResult})
	}
	verificationItems := make([]gin.H, 0, len(verifications))
	for _, verification := range verifications {
		verificationItems = append(verificationItems, gin.H{"id": verification.ID, "source_run_id": verification.SourceRunID, "reproduction_run_id": verification.ReproductionRunID, "status": verification.Status, "input_hash": verification.InputHash, "fingerprint_hash": verification.FingerprintHash, "source_result_hash": verification.SourceResultHash, "reproduction_result_hash": verification.ReproductionResultHash, "created_at": verification.CreatedAt})
	}
	return gin.H{"id": run.ID, "job_id": run.JobID, "revision_id": run.RevisionID, "scenario_definition_id": run.ScenarioDefinitionID, "input_hash": run.InputHash, "fingerprint_hash": run.FingerprintHash, "result_hash": run.ResultHash, "canonical_result": run.CanonicalResult, "metrics": metricItems, "verification_refs": verificationItems, "reproducible": true, "reasons": []string{}, "created_at": run.CreatedAt}
}
