package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	runtimeconfig "github.com/zouyi/eco-guardian/internal/app/runtime/config"
	"github.com/zouyi/eco-guardian/internal/domain"
	storesqlite "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type httpCredentialStore struct{ value []byte }

func (s *httpCredentialStore) Put(_ context.Context, _ string, value []byte) error {
	s.value = append([]byte(nil), value...)
	return nil
}
func (s *httpCredentialStore) Get(context.Context, string) ([]byte, error) {
	if len(s.value) == 0 {
		return nil, aiprovider.ErrCredentialNotFound
	}
	return append([]byte(nil), s.value...), nil
}
func (s *httpCredentialStore) Delete(context.Context, string) error {
	if len(s.value) == 0 {
		return aiprovider.ErrCredentialNotFound
	}
	clear(s.value)
	s.value = nil
	return nil
}

type httpEnvironment map[string]string

func (e httpEnvironment) LookupEnv(name string) (string, bool) {
	value, ok := e[name]
	return value, ok
}

func TestCredentialHandlerIsWriteOnlyAndManagerTakesPrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := &httpCredentialStore{}
	resolver := aiprovider.CredentialResolver{
		Store:       store,
		Environment: httpEnvironment{aiprovider.OpenAIAPIKeyEnvironment: "environment-canary"},
	}
	engine := gin.New()
	NewCredentialHandler(resolver).Register(engine)

	request := httptest.NewRequest(http.MethodPut, "/api/v1/settings/credentials/openai-compatible", strings.NewReader(`{"credential":"manager-canary"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"source":"credential_manager"`) || strings.Contains(response.Body.String(), "manager-canary") {
		t.Fatalf("unsafe credential PUT response=%d body=%s", response.Code, response.Body.String())
	}
	if string(store.value) != "manager-canary" {
		t.Fatal("credential was not written through the credential port")
	}
	replacement := httptest.NewRequest(http.MethodPut, "/api/v1/settings/credentials/openai-compatible", strings.NewReader(`{"credential":"replacement-canary"}`))
	replacement.Header.Set("Content-Type", "application/json")
	replacementResponse := httptest.NewRecorder()
	engine.ServeHTTP(replacementResponse, replacement)
	if replacementResponse.Code != http.StatusOK || string(store.value) != "replacement-canary" || strings.Contains(replacementResponse.Body.String(), "manager-canary") || strings.Contains(replacementResponse.Body.String(), "replacement-canary") {
		t.Fatalf("unsafe credential replacement response=%d body=%s stored=%q", replacementResponse.Code, replacementResponse.Body.String(), store.value)
	}

	response = httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/settings/credentials/openai-compatible", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"credential_present":true`) || !strings.Contains(response.Body.String(), `"source":"environment"`) || strings.Contains(response.Body.String(), "environment-canary") {
		t.Fatalf("unsafe credential DELETE response=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCredentialsAndEnvironmentAreExcludedFromSettingsAndProjectBackupSource(t *testing.T) {
	const managerCanary = "manager-backup-canary"
	const environmentCanary = "environment-backup-canary"
	machineDirectory := t.TempDir()
	projectDirectory := t.TempDir()

	settingsStore := runtimeconfig.NewStore(filepath.Join(machineDirectory, "settings.json"))
	settings := runtimeconfig.Default()
	settings.AI.Enabled = true
	settings.AI.Endpoint = "http://127.0.0.1:11434/v1"
	settings.AI.Model = "fixture"
	if err := settingsStore.Save(settings); err != nil {
		t.Fatal(err)
	}
	credentialStore := &httpCredentialStore{}
	resolver := aiprovider.CredentialResolver{
		Store: credentialStore, Environment: httpEnvironment{aiprovider.OpenAIAPIKeyEnvironment: environmentCanary},
	}
	if err := resolver.Put(context.Background(), aiprovider.OpenAICompatibleProvider, []byte(managerCanary)); err != nil {
		t.Fatal(err)
	}
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	projectStore, _, err := storesqlite.Create(context.Background(), projectDirectory, registry)
	if err != nil {
		t.Fatal(err)
	}
	if err := projectStore.Close(); err != nil {
		t.Fatal(err)
	}

	for _, root := range []string{machineDirectory, projectDirectory} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, canary := range []string{managerCanary, environmentCanary} {
				if strings.Contains(string(contents), canary) {
					t.Errorf("credential leaked into backup source %s", filepath.Base(path))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	settingsJSON, err := json.Marshal(settings)
	if err != nil || strings.Contains(string(settingsJSON), managerCanary) || strings.Contains(string(settingsJSON), environmentCanary) {
		t.Fatalf("settings projection leaked credential: %s err=%v", settingsJSON, err)
	}
}

func TestCredentialHandlerRejectsUnknownFieldsProvidersAndSecretEcho(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewCredentialHandler(aiprovider.CredentialResolver{Store: &httpCredentialStore{}}).Register(engine)
	for _, test := range []struct {
		path string
		body string
	}{
		{"/api/v1/settings/credentials/openai-compatible", `{"credential":"secret-canary","extra":true}`},
		{"/api/v1/settings/credentials/unknown", `{"credential":"secret-canary"}`},
		{"/api/v1/settings/credentials/openai-compatible", `{"credential":"line\nbreak"}`},
	} {
		request := httptest.NewRequest(http.MethodPut, test.path, strings.NewReader(test.body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "secret-canary") || strings.Contains(response.Body.String(), "line") {
			t.Fatalf("invalid credential response=%d body=%s", response.Code, response.Body.String())
		}
	}
	foreign := httptest.NewRequest(http.MethodDelete, "/api/v1/settings/credentials/openai-compatible", nil)
	foreign.Header.Set("Origin", "https://evil.example")
	foreignResponse := httptest.NewRecorder()
	engine.ServeHTTP(foreignResponse, foreign)
	if foreignResponse.Code != http.StatusForbidden {
		t.Fatalf("foreign credential origin response=%d body=%s", foreignResponse.Code, foreignResponse.Body.String())
	}
}
