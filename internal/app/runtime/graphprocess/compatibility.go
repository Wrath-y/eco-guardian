package graphprocess

import (
	"net/http"
	"sort"

	graphsync "github.com/zouyi/eco-guardian/internal/graph/sync"
)

type OperationState string

const (
	OperationAvailable   OperationState = "available"
	OperationDegraded    OperationState = "degraded"
	OperationUnavailable OperationState = "unavailable"
)

type OperationCompatibility struct {
	ID      OperationID
	State   OperationState
	Reasons []string
}

type HealthCompatibility struct {
	Compatible      bool
	Status          string
	ObservedVersion string
	RequiredVersion string
	Reasons         []string
	Diagnostics     []string
	Operations      []OperationCompatibility
	Retrieval       OperationCompatibility
}

// EvaluateHealthCompatibility is a pure reducer. It interprets only typed
// health fields and the observed HTTP status; provider messages and unknown
// additive fields never participate in compatibility decisions.
func EvaluateHealthCompatibility(health graphsync.Health, registry *OperationRegistry) HealthCompatibility {
	return evaluateHealthCompatibility(health, registry, CompiledKnownFixTable)
}

func evaluateHealthCompatibility(health graphsync.Health, registry *OperationRegistry, knownFixes KnownFixTable) HealthCompatibility {
	if registry == nil {
		registry = DefaultOperationRegistry()
	}
	result := HealthCompatibility{Status: health.Status, ObservedVersion: health.ServiceVersion, Reasons: []string{}, Diagnostics: []string{}, Operations: []OperationCompatibility{}}
	knownFix := knownFixes.Evaluate(health.ServiceVersion)
	result.Diagnostics = append(result.Diagnostics, knownFix.Diagnostics...)
	result.RequiredVersion = knownFix.RequiredVersion
	if !knownFix.Allowed {
		result.Reasons = append(result.Reasons, "KNOWN_FIX_REQUIRED")
		if knownFix.Reason != "" {
			result.Diagnostics = append(result.Diagnostics, knownFix.Reason)
		}
	}
	if health.Service != "local-rag" {
		result.Reasons = append(result.Reasons, "SERVICE_IDENTITY_INVALID")
	}
	if health.SchemaVersion != "1.0" {
		result.Reasons = append(result.Reasons, "HEALTH_SCHEMA_UNSUPPORTED")
	}
	if !containsString(health.APIVersions, "v1") {
		result.Reasons = append(result.Reasons, "API_V1_UNSUPPORTED")
	}
	if !containsString(health.SupportedSchemaVersions, "1.0") {
		result.Reasons = append(result.Reasons, "SNAPSHOT_SCHEMA_UNSUPPORTED")
	}
	switch health.Status {
	case "ok", "degraded":
		if health.HTTPStatus != 0 && health.HTTPStatus != http.StatusOK {
			result.Reasons = append(result.Reasons, "HTTP_STATUS_MISMATCH")
		}
	case "unavailable":
		if health.HTTPStatus != 0 && health.HTTPStatus != http.StatusServiceUnavailable {
			result.Reasons = append(result.Reasons, "HTTP_STATUS_MISMATCH")
		}
		result.Reasons = append(result.Reasons, "HEALTH_UNAVAILABLE")
	default:
		result.Reasons = append(result.Reasons, "HEALTH_STATUS_INVALID")
	}
	capabilities, capabilityDuplicates := healthStateMap(health.Capabilities)
	dependencies, dependencyDuplicates := healthStateMap(health.Dependencies)
	limits, limitDuplicates := healthLimitMap(health.Limits)
	if capabilityDuplicates {
		result.Reasons = append(result.Reasons, "CAPABILITY_DUPLICATE")
	}
	if dependencyDuplicates {
		result.Reasons = append(result.Reasons, "DEPENDENCY_DUPLICATE")
	}
	if limitDuplicates {
		result.Reasons = append(result.Reasons, "LIMIT_DUPLICATE")
	}
	baseUnavailable := len(result.Reasons) != 0
	for _, descriptor := range registry.Descriptors() {
		operation := OperationCompatibility{ID: descriptor.ID, State: OperationAvailable, Reasons: []string{}}
		if baseUnavailable {
			operation.State = OperationUnavailable
			operation.Reasons = append(operation.Reasons, result.Reasons...)
		} else {
			for _, name := range descriptor.RequiredCapabilities {
				applyHealthState(&operation, "CAPABILITY_"+name, capabilities[name])
			}
			for _, name := range descriptor.RequiredDependencies {
				applyHealthState(&operation, "DEPENDENCY_"+name, dependencies[name])
			}
			for _, requirement := range descriptor.RequiredLimits {
				if value, ok := limits[requirement.Name]; !ok || value < requirement.Minimum {
					operation.State = OperationUnavailable
					operation.Reasons = append(operation.Reasons, "LIMIT_"+requirement.Name)
				}
			}
		}
		sort.Strings(operation.Reasons)
		result.Operations = append(result.Operations, operation)
	}
	result.Retrieval = reduceRetrievalCompatibility(result.Operations)
	sort.Strings(result.Reasons)
	sort.Strings(result.Diagnostics)
	result.Compatible = len(result.Reasons) == 0 && requiredCoreOperationsAvailable(result.Operations)
	return result
}

func reduceRetrievalCompatibility(operations []OperationCompatibility) OperationCompatibility {
	result := OperationCompatibility{ID: "retrieval", State: OperationAvailable, Reasons: []string{}}
	bm25 := operationByID(operations, OperationRetrievalBM25)
	vector := operationByID(operations, OperationRetrievalVector)
	rerank := operationByID(operations, OperationRetrievalRerank)
	if bm25.State == OperationUnavailable && vector.State == OperationUnavailable {
		result.State = OperationUnavailable
	} else if bm25.State != OperationAvailable || vector.State != OperationAvailable || rerank.State != OperationAvailable {
		result.State = OperationDegraded
	}
	for _, operation := range []OperationCompatibility{bm25, vector, rerank} {
		if operation.State != OperationAvailable {
			result.Reasons = append(result.Reasons, operation.Reasons...)
		}
	}
	sort.Strings(result.Reasons)
	return result
}

func applyHealthState(operation *OperationCompatibility, prefix, state string) {
	switch state {
	case "available":
	case "degraded":
		if operation.State == OperationAvailable {
			operation.State = OperationDegraded
		}
		operation.Reasons = append(operation.Reasons, prefix+"_DEGRADED")
	case "unavailable", "disabled", "":
		operation.State = OperationUnavailable
		operation.Reasons = append(operation.Reasons, prefix+"_UNAVAILABLE")
	default:
		operation.State = OperationUnavailable
		operation.Reasons = append(operation.Reasons, prefix+"_INVALID")
	}
}

func requiredCoreOperationsAvailable(operations []OperationCompatibility) bool {
	required := map[OperationID]bool{OperationSnapshotLifecycle: true, OperationTaskPolling: true, OperationCoreQuery: true, OperationGraphFTSReadiness: true}
	for _, operation := range operations {
		if required[operation.ID] {
			if operation.State == OperationUnavailable {
				return false
			}
			delete(required, operation.ID)
		}
	}
	return len(required) == 0
}

func healthStateMap(values []graphsync.HealthState) (map[string]string, bool) {
	result := map[string]string{}
	duplicate := false
	for _, value := range values {
		if _, exists := result[value.Name]; exists || value.Name == "" {
			duplicate = true
		}
		result[value.Name] = value.State
	}
	return result, duplicate
}

func healthLimitMap(values []graphsync.Limit) (map[string]int, bool) {
	result := map[string]int{}
	duplicate := false
	for _, value := range values {
		if _, exists := result[value.Name]; exists || value.Name == "" || value.Value < 0 {
			duplicate = true
		}
		result[value.Name] = value.Value
	}
	return result, duplicate
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func operationByID(values []OperationCompatibility, id OperationID) OperationCompatibility {
	for _, value := range values {
		if value.ID == id {
			return value
		}
	}
	return OperationCompatibility{ID: id, State: OperationUnavailable, Reasons: []string{"OPERATION_UNREGISTERED"}}
}
