package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
)

func TestSettingsHandlerReturnsOnlyNonSensitiveProviderMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := runtimeconfig.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	engine := gin.New()
	NewSettingsHandler(store, func(provider string) bool { return provider == "openai-compatible" }).Register(engine)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"credential_present":true`) || strings.Contains(strings.ToLower(response.Body.String()), "api_key") || strings.Contains(strings.ToLower(response.Body.String()), "secret") {
		t.Fatalf("unsafe settings response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSettingsHandlerStrictlyUpdatesBoundedAIReference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := runtimeconfig.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	engine := gin.New()
	NewSettingsHandler(store, nil).Register(engine)
	body := `{"ai":{"enabled":true,"endpoint":"http://127.0.0.1:11434/v1","model":"fixture","request_timeout_seconds":90,"allow_cloud":false}}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"endpoint_classification":"loopback"`) {
		t.Fatalf("settings patch response=%d body=%s", response.Code, response.Body.String())
	}
	settings, _, err := store.Load()
	if err != nil || !settings.AI.Enabled || settings.AI.Model != "fixture" || settings.AI.RequestTimeoutSeconds != 90 {
		t.Fatalf("persisted settings=%#v err=%v", settings, err)
	}

	bad := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(`{"ai":{"enabled":false,"endpoint":"","model":"","request_timeout_seconds":120,"allow_cloud":false,"api_key":"secret"}}`))
	bad.Header.Set("Content-Type", "application/json")
	badResponse := httptest.NewRecorder()
	engine.ServeHTTP(badResponse, bad)
	if badResponse.Code != http.StatusBadRequest || strings.Contains(badResponse.Body.String(), "secret") {
		t.Fatalf("credential field response=%d body=%s", badResponse.Code, badResponse.Body.String())
	}
}
