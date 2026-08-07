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
	for _, id := range []string{"selectProjectDirectory", "openProject", "listRecentProjects", "openRecentProject", "getCurrentProject", "closeProject", "getEntitySchema", "listEntities", "createEntity", "getEntity", "patchEntity", "deleteEntity", "createValidationRun", "getValidationRun", "listRevisions", "createRevision", "getRevision", "getRevisionDiff", "listReleasePolicies", "createReleasePolicy", "listReleases", "createRelease", "getRelease", "getJob", "streamJobEvents", "cancelJob", "getRuntimeCapabilities"} {
		if !strings.Contains(contract, "operationId: "+id) {
			t.Errorf("OpenAPI missing handler operation %s", id)
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
