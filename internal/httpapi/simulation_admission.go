package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/app"
	"github.com/zouyi/eco-guardian/internal/domain"
	sharedjob "github.com/zouyi/eco-guardian/internal/job"
	"github.com/zouyi/eco-guardian/internal/project"
	"github.com/zouyi/eco-guardian/internal/simulation/contract"
	simulationgate "github.com/zouyi/eco-guardian/internal/simulation/gate"
	"github.com/zouyi/eco-guardian/internal/simulation/orchestration"
	"github.com/zouyi/eco-guardian/internal/simulation/scenario"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
	"github.com/zouyi/eco-guardian/internal/validation"
)

type SimulationAdmissionServiceProvider func() app.SimulationAdmissionService

// SimulationAdmissionServiceFromProjectManager is the only HTTP composition
// seam that combines the active Store with the registered current manifests.
func SimulationAdmissionServiceFromProjectManager(manager *project.Manager) SimulationAdmissionServiceProvider {
	return func() app.SimulationAdmissionService {
		handle, ok := manager.ActiveHandle()
		if !ok {
			return nil
		}
		provider, ok := handle.(interface{ Store() *store.Store })
		if !ok {
			return nil
		}
		s := provider.Store()
		registry, err := contract.NewManifestRegistry(contract.RequiredV1Descriptors, contract.V1Descriptors())
		if err != nil {
			return nil
		}
		return app.SimulationAdmissionApplication{Revisions: s, Releases: s, Gate: simulationgate.FullValidationAdapter{Revisions: s, Gate: validation.NewValidationGate(s)}, Scenarios: s, Fingerprints: app.SimulationFingerprintResolver{Revisions: s, Registry: registry}, Jobs: app.SimulationApplication{Jobs: s, Materializations: s}}
	}
}

type SimulationAdmissionHandler struct {
	service SimulationAdmissionServiceProvider
	jobs    SimulationJobStoreProvider
}

func NewSimulationAdmissionHandler(service SimulationAdmissionServiceProvider, jobs SimulationJobStoreProvider) *SimulationAdmissionHandler {
	return &SimulationAdmissionHandler{service: service, jobs: jobs}
}

func (h *SimulationAdmissionHandler) Register(r *gin.Engine) {
	r.POST("/api/v1/simulation-jobs", h.create)
}

func (h *SimulationAdmissionHandler) create(c *gin.Context) {
	if h.service == nil || h.jobs == nil || h.service() == nil || h.jobs() == nil {
		problem(c, http.StatusServiceUnavailable, "SIMULATION_CAPABILITY_UNAVAILABLE", "Simulation capability is unavailable")
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if key == "" {
		problem(c, http.StatusBadRequest, "SIMULATION_IDEMPOTENCY_REQUIRED", "Idempotency-Key is required")
		return
	}
	var request struct {
		Source struct {
			RevisionID domain.ID `json:"revision_id"`
			ReleaseID  domain.ID `json:"release_id"`
		} `json:"source"`
		SceneID      string                     `json:"scene_id"`
		SceneVersion string                     `json:"scene_version"`
		Metrics      []contract.MetricIdentity  `json:"metrics"`
		SampleCount  int                        `json:"sample_count"`
		Seed         *uint64                    `json:"seed"`
		Parameters   map[string]json.RawMessage `json:"parameters"`
		Budget       map[string]any             `json:"budget"`
		VerifyRunID  domain.ID                  `json:"verify_run_id"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || len(request.Budget) != 0 || request.VerifyRunID != "" {
		problem(c, http.StatusBadRequest, "SIMULATION_INPUT_INVALID", "Simulation request is invalid")
		return
	}
	reader := h.jobs()
	// Store readers are project-scoped; derive the active project from the
	// service's only valid source rather than accepting a client project ID.
	provider, ok := reader.(interface{ ProjectID() domain.ID })
	if !ok || !provider.ProjectID().Valid() {
		problem(c, http.StatusServiceUnavailable, "SIMULATION_CAPABILITY_UNAVAILABLE", "Simulation capability is unavailable")
		return
	}
	projectID := provider.ProjectID()
	paths := make([]string, 0, len(request.Parameters))
	for path := range request.Parameters {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	parameters := make([]scenario.ParameterOverlay, 0, len(paths))
	for _, path := range paths {
		parameters = append(parameters, scenario.ParameterOverlay{Path: path, Value: append(json.RawMessage(nil), request.Parameters[path]...)})
	}
	result, err := h.service().AdmitSimulation(c.Request.Context(), app.SimulationAdmission{ProjectID: projectID, RevisionID: request.Source.RevisionID, ReleaseID: request.Source.ReleaseID, SceneID: request.SceneID, SceneVersion: request.SceneVersion, Metrics: request.Metrics, SampleCount: request.SampleCount, Seed: request.Seed, Parameters: parameters, IdempotencyKey: key})
	if err != nil {
		writeSimulationAdmissionError(c, err)
		return
	}
	job, err := reader.GetJob(c.Request.Context(), result.Job)
	if err != nil || job.Kind != sharedjob.Kind("simulation") {
		problem(c, http.StatusServiceUnavailable, "SIMULATION_CAPABILITY_UNAVAILABLE", "Simulation capability is unavailable")
		return
	}
	location := "/api/v1/jobs/" + string(job.ID)
	c.Header("Location", location)
	c.JSON(http.StatusAccepted, gin.H{"job": simulationJobJSON(job), "location": location})
}

func writeSimulationAdmissionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, orchestration.ErrFullValidationRequired):
		problem(c, http.StatusConflict, "SIMULATION_VALIDATION_REQUIRED", "Simulation requires matching FULL validation")
	case errors.Is(err, contract.ErrSourceInvalid), errors.Is(err, contract.ErrSourceOwnership):
		problem(c, http.StatusBadRequest, "SIMULATION_SOURCE_INVALID", "Simulation source is invalid")
	case errors.Is(err, contract.ErrInputInvalid):
		problem(c, http.StatusBadRequest, "SIMULATION_INPUT_INVALID", "Simulation request is invalid")
	case errors.Is(err, store.ErrJobIdempotencyConflict):
		problem(c, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key conflicts with a different simulation")
	default:
		problem(c, http.StatusServiceUnavailable, "SIMULATION_CAPABILITY_UNAVAILABLE", "Simulation capability is unavailable")
	}
}
