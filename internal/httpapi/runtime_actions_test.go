package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
)

type fakeRuntimeActionService struct {
	mu            sync.Mutex
	preconditions map[string]bool
	reprobes      int
	reconnects    int
	err           error
}

func (service *fakeRuntimeActionService) Reprobe(context.Context) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.reprobes++
	return service.err
}

func (service *fakeRuntimeActionService) Reconnect(context.Context) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.reconnects++
	return service.err
}

func (service *fakeRuntimeActionService) RuntimeActionPreconditions() map[string]bool {
	return service.preconditions
}

func TestRuntimeActionsEnforcePreconditionsAndIdempotency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeRuntimeActionService{preconditions: map[string]bool{"dependency_observed": true, "graph_configured": true}}
	engine := gin.New()
	NewRuntimeActionHandler(service).Register(engine)

	request := func(path, key string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, nil)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		engine.ServeHTTP(response, req)
		return response
	}
	if response := request("/api/v1/runtime/reprobe", ""); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "IDEMPOTENCY_KEY_INVALID") {
		t.Fatalf("missing key response=%d %s", response.Code, response.Body.String())
	}
	if response := request("/api/v1/runtime/reprobe", "probe-1"); response.Code != http.StatusNoContent {
		t.Fatalf("reprobe response=%d %s", response.Code, response.Body.String())
	}
	if response := request("/api/v1/runtime/reprobe", "probe-1"); response.Code != http.StatusNoContent {
		t.Fatalf("duplicate response=%d %s", response.Code, response.Body.String())
	}
	service.mu.Lock()
	if service.reprobes != 1 {
		t.Fatalf("duplicate idempotency key executed %d probes", service.reprobes)
	}
	service.mu.Unlock()

	service.preconditions["graph_configured"] = false
	if response := request("/api/v1/runtime/reconnect", "reconnect-1"); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "RUNTIME_ACTION_PRECONDITION_FAILED") {
		t.Fatalf("precondition response=%d %s", response.Code, response.Body.String())
	}
}

func TestRuntimeActionFailureIsSafeAndCached(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeRuntimeActionService{
		preconditions: map[string]bool{"dependency_observed": true, "graph_configured": true},
		err:           errors.New("unsafe provider detail"),
	}
	engine := gin.New()
	NewRuntimeActionHandler(service).Register(engine)
	for range 2 {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/runtime/reconnect", nil)
		request.Header.Set("Idempotency-Key", "same-failure")
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "unsafe provider detail") {
			t.Fatalf("response=%d %s", response.Code, response.Body.String())
		}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.reconnects != 1 {
		t.Fatalf("cached failure executed %d reconnects", service.reconnects)
	}
}
