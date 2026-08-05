package httpapi

import (
	"os"
	"strings"
	"testing"
)

func TestOpenAPIContainsAllHandlerOperations(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	contract := string(raw)
	for _, id := range []string{"selectProjectDirectory", "openProject", "listRecentProjects", "openRecentProject", "getCurrentProject", "closeProject", "getEntitySchema", "listEntities", "createEntity", "getEntity", "patchEntity", "deleteEntity", "createValidationRun", "getValidationRun"} {
		if !strings.Contains(contract, "operationId: "+id) {
			t.Errorf("OpenAPI missing handler operation %s", id)
		}
	}
}
