package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
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

	response = httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/settings/credentials/openai-compatible", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"credential_present":true`) || !strings.Contains(response.Body.String(), `"source":"environment"`) || strings.Contains(response.Body.String(), "environment-canary") {
		t.Fatalf("unsafe credential DELETE response=%d body=%s", response.Code, response.Body.String())
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
}
