package httpapi

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestOpenAPIContainsAllHandlerOperations(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, id := range []string{"selectProjectDirectory", "openProject", "listRecentProjects", "openRecentProject", "getCurrentProject", "closeProject", "getEntitySchema", "listEntities", "createEntity", "getEntity", "patchEntity", "deleteEntity", "createValidationRun", "getValidationRun", "listRevisions", "createRevision", "getRevision", "getRevisionDiff", "ensureGraphSync", "getGraphStatus", "listReleasePolicies", "createReleasePolicy", "listReleases", "createRelease", "getRelease", "getJob", "streamJobEvents", "cancelJob", "createSimulationJob", "getSimulationRun", "getRuntimeCapabilities"} {
		if !strings.Contains(contract, "operationId: "+id) {
			t.Errorf("OpenAPI missing handler operation %s", id)
		}
	}
}

func TestOpenAPIContainsGraphStatusAndProblemContracts(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, schema := range []string{
		"GraphSyncRequest", "GraphSyncJobAccepted", "GraphStatus", "GraphProjectionSummary",
		"GraphProviderObservation", "GraphComponent", "GraphEvidence", "GraphSafeError",
	} {
		if !strings.Contains(contract, "    "+schema+":") {
			t.Errorf("OpenAPI missing graph schema %s", schema)
		}
	}
	for _, code := range []string{
		"GRAPH_VALIDATION_REQUIRED", "GRAPH_VALIDATION_BLOCKED", "PROJECTOR_VERSION_UNAVAILABLE",
		"GRAPH_CAPABILITY_UNAVAILABLE", "CONTENT_HASH_MISMATCH", "CONTENT_HASH_CONFLICT",
		"PROVIDER_TASK_FAILED", "GRAPH_RETRY_EXHAUSTED",
	} {
		if !strings.Contains(contract, code) {
			t.Errorf("OpenAPI missing graph problem code %s", code)
		}
	}
}

func TestOpenAPIContainsSimulationProblemContracts(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, code := range []string{
		"SIMULATION_IDEMPOTENCY_REQUIRED", "SIMULATION_INPUT_INVALID", "SIMULATION_SEED_INVALID",
		"SIMULATION_VALIDATION_REQUIRED", "SIMULATION_SOURCE_INVALID", "SIMULATION_SCENE_INVALID",
		"SIMULATION_PARAMETER_INVALID", "SIMULATION_METRIC_INVALID", "SIMULATION_SAMPLE_INVALID",
		"SIMULATION_BUDGET_INVALID", "SIMULATION_IMPLEMENTATION_UNAVAILABLE",
		"SIMULATION_VERIFICATION_TARGET_INVALID", "SIMULATION_CAPABILITY_UNAVAILABLE",
		"SIMULATION_RUN_NOT_FOUND", "SIMULATION_RUN_UNAVAILABLE", "BUDGET_EXCEEDED", "TIMEOUT",
		"RECOVERY_MISMATCH", "RECOVERY_UNAVAILABLE",
	} {
		if !strings.Contains(contract, code) {
			t.Errorf("OpenAPI missing simulation problem code %s", code)
		}
	}
}

func TestOpenAPISharedJobContractIncludesRegisteredKindsAndCancellation(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, fragment := range []string{"Registered durable Job kind", "cancel_generation:", "cancel_requested_at:", "Last-Event-ID"} {
		if !strings.Contains(contract, fragment) {
			t.Errorf("OpenAPI missing shared Job contract fragment %q", fragment)
		}
	}
}

func TestVersioningDTOsAreGeneratedRatherThanHandwritten(t *testing.T) {
	duplicate := regexp.MustCompile(`(?m)^(?:export\s+)?(?:interface|type)\s+(?:Revision|Release|Job|Gate|VersionManifest)\w*`)
	for _, root := range []string{".", "../../web/src/features/versions"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || path == "contract_test.go" {
				return nil
			}
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if duplicate.Match(contents) {
				t.Errorf("handwritten versioning DTO in %s; use generated client DTOs", path)
			}
			return nil
		})
		if root == "../../web/src/features/versions" && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestSimulationClientUsesGeneratedDTOsRatherThanHandwrittenDuplicates(t *testing.T) {
	path := "../../web/src/api/simulation.ts"
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	if !strings.Contains(text, "import type { components } from './generated'") {
		t.Fatal("simulation client must use OpenAPI-generated component schemas")
	}
	duplicate := regexp.MustCompile(`(?m)^(?:export\s+)?(?:interface\s+Simulation\w*|type\s+Simulation\w*\s*=\s*\{)`)
	if duplicate.MatchString(text) {
		t.Fatal("simulation client declares a handwritten DTO; use generated component schemas")
	}
}

func TestVersioningProblemFixturesHaveStableStatusMappings(t *testing.T) {
	raw, err := os.ReadFile("../../api/fixtures/problems.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"REVISION_NOT_FOUND": 404, "REVISION_IMMUTABLE": 409, "REVISION_CONFLICT": 409,
		"DIFF_BASE_INVALID": 400, "RELEASE_POLICY_INVALID": 400, "RELEASE_CAPABILITY_DISABLED": 409,
		"RELEASE_PREFLIGHT_FAILED": 422, "RELEASE_BASE_CONFLICT": 409, "IDEMPOTENCY_CONFLICT": 409,
		"MANDATORY_BACKUP_FAILED": 502, "MIGRATION_BACKUP_REQUIRED": 409, "INVALID_CONFIRMATION": 400,
		"HISTORY_NOT_FOUND": 404, "STORAGE_FAILURE": 500, "EXTERNAL_SERVICE_FAILURE": 503,
		"GRAPH_VALIDATION_REQUIRED": 409, "GRAPH_VALIDATION_BLOCKED": 409,
		"PROJECTOR_VERSION_UNAVAILABLE": 409, "GRAPH_CAPABILITY_UNAVAILABLE": 503,
		"BASE_SNAPSHOT_NOT_FOUND": 409, "BASE_SNAPSHOT_NOT_READY": 409,
		"CONTENT_HASH_MISMATCH": 422, "CONTENT_HASH_CONFLICT": 409,
		"PROVIDER_TASK_FAILED": 422, "GRAPH_RETRY_EXHAUSTED": 503,
		"GRAPH_RETRY_NOT_SAFE": 409, "GRAPH_STATUS_UNAVAILABLE": 503,
		"SIMULATION_IDEMPOTENCY_REQUIRED": 400, "SIMULATION_INPUT_INVALID": 400, "SIMULATION_SEED_INVALID": 400,
		"SIMULATION_VALIDATION_REQUIRED": 409, "SIMULATION_SOURCE_INVALID": 400,
		"SIMULATION_SCENE_INVALID": 400, "SIMULATION_PARAMETER_INVALID": 400,
		"SIMULATION_METRIC_INVALID": 400, "SIMULATION_SAMPLE_INVALID": 400,
		"SIMULATION_BUDGET_INVALID": 400, "SIMULATION_IMPLEMENTATION_UNAVAILABLE": 409,
		"SIMULATION_VERIFICATION_TARGET_INVALID": 400, "SIMULATION_CAPABILITY_UNAVAILABLE": 503,
		"SIMULATION_RUN_NOT_FOUND": 404, "SIMULATION_RUN_UNAVAILABLE": 503,
		"BUDGET_EXCEEDED": 422, "TIMEOUT": 504, "RECOVERY_MISMATCH": 409, "RECOVERY_UNAVAILABLE": 409,
	}
	got := map[string]int{}
	for _, fixture := range fixtures {
		got[fixture.Code] = fixture.Status
	}
	for code, status := range want {
		if got[code] != status {
			t.Errorf("%s status=%d, want %d", code, got[code], status)
		}
	}
}

func TestOpenAPIContainsRevisionContractSchemas(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, schema := range []string{
		"RevisionVersionManifest", "RevisionMetadata", "RevisionHistoryItem", "RevisionDetail",
		"RevisionPage", "RevisionTimeline", "CurrentWorkingPrecondition", "CreateRevisionRequest",
	} {
		if !strings.Contains(contract, "    "+schema+":") {
			t.Errorf("OpenAPI missing revision schema %s", schema)
		}
	}
}
