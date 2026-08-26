package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
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
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"endpoint_classification":"loopback"`) || !strings.Contains(response.Body.String(), `"disposition":"reconnect_required"`) {
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
	foreign := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(body))
	foreign.Header.Set("Content-Type", "application/json")
	foreign.Header.Set("Origin", "https://evil.example")
	foreignResponse := httptest.NewRecorder()
	engine.ServeHTTP(foreignResponse, foreign)
	if foreignResponse.Code != http.StatusForbidden {
		t.Fatalf("foreign origin response=%d body=%s", foreignResponse.Code, foreignResponse.Body.String())
	}
}

func TestSettingsHandlerAtomicallyUpdatesRuntimeSettingsAndReportsEffects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := runtimeconfig.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	engine := gin.New()
	NewSettingsHandler(store, nil).Register(engine)
	body := `{"browser":{"auto_open":false},"graph":{"mode":"external","endpoint":"http://127.0.0.1:9300","health_timeout_seconds":8,"startup_timeout_seconds":45,"restart_limit":2},"logs":{"max_bytes":8388608,"max_files":4},"backup":{"retention_days":60}}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("settings patch response=%d body=%s", response.Code, response.Body.String())
	}
	var result riskdto.SettingsUpdateResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	wantEffects := []riskdto.SettingsApplyEffect{
		{Field: "browser", Disposition: riskdto.Applied},
		{Field: "graph", Disposition: riskdto.ReconnectRequired},
		{Field: "logs", Disposition: riskdto.RestartRequired},
		{Field: "backup", Disposition: riskdto.Applied},
	}
	if !reflect.DeepEqual(result.Effects, wantEffects) {
		t.Errorf("settings effects=%#v want=%#v", result.Effects, wantEffects)
	}
	persisted, _, err := store.Load()
	if err != nil || persisted.Browser.AutoOpen || persisted.Graph.Endpoint != "http://127.0.0.1:9300" || persisted.Logs.MaxFiles != 4 || persisted.Backup.RetentionDays != 60 {
		t.Fatalf("persisted settings=%#v err=%v", persisted, err)
	}
}

func TestSettingsHandlerReturnsFieldProblemAndPreservesPriorDocument(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := runtimeconfig.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	before := runtimeconfig.Default()
	before.Browser.AutoOpen = false
	if err := store.Save(before); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	NewSettingsHandler(store, nil).Register(engine)
	body := `{"graph":{"mode":"external","endpoint":"http://example.test:9300","health_timeout_seconds":8,"startup_timeout_seconds":45,"restart_limit":2}}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"SETTINGS_INVALID"`) || !strings.Contains(response.Body.String(), `"field_path":"graph.endpoint"`) || strings.Contains(response.Body.String(), "example.test") {
		t.Fatalf("settings error response=%d body=%s", response.Code, response.Body.String())
	}
	after, _, err := store.Load()
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("prior settings changed: before=%#v after=%#v err=%v", before, after, err)
	}
}

func TestSettingsHandlerRequiresCloudDisclosureAndReportsClassification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := runtimeconfig.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	engine := gin.New()
	NewSettingsHandler(store, nil).Register(engine)

	for _, test := range []struct {
		allow  bool
		status int
	}{
		{false, http.StatusBadRequest},
		{true, http.StatusOK},
	} {
		body := `{"ai":{"enabled":true,"endpoint":"https://api.example.com/v1","model":"fixture","request_timeout_seconds":90,"allow_cloud":` + strconv.FormatBool(test.allow) + `}}`
		request := httptest.NewRequest(http.MethodPatch, "/api/v1/settings", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("allow_cloud=%v response=%d body=%s", test.allow, response.Code, response.Body.String())
		}
		if test.status == http.StatusOK && !strings.Contains(response.Body.String(), `"endpoint_classification":"cloud"`) {
			t.Fatalf("cloud classification missing: %s", response.Body.String())
		}
	}
}

func TestProblemDetailsRedactSensitiveAssignments(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/problem", func(c *gin.Context) {
		problemDetails(c, http.StatusBadGateway, "AI_PROVIDER_FAILED", "Provider authorization=secret-canary failed", map[string]any{
			"provider_error": "Bearer secret-canary",
			"api_key":        "secret-canary",
		}, "")
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/problem", nil))
	if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "secret-canary") || !strings.Contains(response.Body.String(), "[REDACTED]") {
		t.Fatalf("unsafe Problem Details: %s", response.Body.String())
	}
}
