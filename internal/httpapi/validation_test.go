package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

func TestValidationRequestsUseProblemDetailsAndImmutableGet(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	project, _, err := store.Create(context.Background(), t.TempDir(), registry)
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	engine := gin.New()
	NewValidationHandler(func() *store.Store { return project }).Register(engine)

	invalid := httptest.NewRequest(http.MethodPost, "/api/v1/validation/runs", strings.NewReader(`{"source":{"type":"both"},"scope":"FULL"}`))
	invalid.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, invalid)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"INVALID_VALIDATION_SOURCE"`) {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}

	valid := httptest.NewRequest(http.MethodPost, "/api/v1/validation/runs", strings.NewReader(`{"source":{"type":"working"},"scope":"FULL"}`))
	valid.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, valid)
	if response.Code != http.StatusCreated {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	var body struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.ID == "" {
		t.Fatalf("%s %v", response.Body.String(), err)
	}
	get := httptest.NewRequest(http.MethodGet, "/api/v1/validation/runs/"+body.ID, nil)
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, get)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"completed"`) {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}

func TestValidationRequestProblemMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	project, _, err := store.Create(context.Background(), t.TempDir(), registry)
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	engine := gin.New()
	NewValidationHandler(func() *store.Store { return project }).Register(engine)
	missingRevision, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, body, code string
		status           int
	}{
		{"malformed", `{`, "INVALID_VALIDATION_REQUEST", http.StatusBadRequest},
		{"working revision", `{"source":{"type":"working","revision_id":"` + string(missingRevision) + `"},"scope":"FULL"}`, "INVALID_VALIDATION_SOURCE", http.StatusBadRequest},
		{"revision missing id", `{"source":{"type":"revision"},"scope":"FULL"}`, "INVALID_VALIDATION_SOURCE", http.StatusBadRequest},
		{"bad scope", `{"source":{"type":"working"},"scope":"BASE"}`, "INVALID_VALIDATION_SCOPE", http.StatusBadRequest},
		{"local no target", `{"source":{"type":"working"},"scope":"LOCAL"}`, "INVALID_VALIDATION_TARGET", http.StatusBadRequest},
		{"full target", `{"source":{"type":"working"},"scope":"FULL","entity_ids":["` + string(missingRevision) + `"]}`, "INVALID_VALIDATION_TARGET", http.StatusBadRequest},
		{"missing revision", `{"source":{"type":"revision","revision_id":"` + string(missingRevision) + `"},"scope":"FULL"}`, "VALIDATION_SOURCE_NOT_FOUND", http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/validation/runs", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Request-ID", "request-123")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) || !strings.Contains(response.Body.String(), `"request_id":"request-123"`) {
				t.Fatalf("%d %s", response.Code, response.Body.String())
			}
		})
	}
	unknown, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/validation/runs/"+string(unknown), nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"VALIDATION_RUN_NOT_FOUND"`) {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}

func TestValidationRequiresAnOpenProject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewValidationHandler(func() *store.Store { return nil }).Register(engine)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/validation/runs", strings.NewReader(`{"source":{"type":"working"},"scope":"FULL"}`))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"PROJECT_NOT_OPEN"`) {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}
