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
	for _, id := range []string{"selectProjectDirectory", "openProject", "listRecentProjects", "openRecentProject", "getCurrentProject", "closeProject", "getEntitySchema", "listEntities", "createEntity", "getEntity", "patchEntity", "deleteEntity", "createValidationRun", "getValidationRun", "listRevisions", "createRevision", "getRevision", "getRevisionDiff", "ensureGraphSync", "getGraphStatus", "listReleasePolicies", "createReleasePolicy", "listReleases", "createRelease", "getRelease", "getJob", "streamJobEvents", "cancelJob", "getRuntimeCapabilities"} {
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
