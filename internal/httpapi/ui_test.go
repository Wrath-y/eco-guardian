package httpapi

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func TestEmbeddedUIRoutingAndCaching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	assets := fstest.MapFS{
		"web/dist/index.html":           &fstest.MapFile{Data: []byte("<html>shell</html>")},
		"web/dist/assets/index-abc.js":  &fstest.MapFile{Data: []byte("export const ready = true")},
		"web/dist/assets/index-abc.css": &fstest.MapFile{Data: []byte("body { color: green }")},
	}
	engine := gin.New()
	RegisterEmbeddedUI(engine, fs.FS(assets))

	tests := []struct {
		name         string
		path         string
		status       int
		contentType  string
		cacheControl string
		body         string
	}{
		{name: "root shell", path: "/", status: http.StatusOK, contentType: "text/html", cacheControl: "no-store", body: "shell"},
		{name: "history route shell", path: "/backups", status: http.StatusOK, contentType: "text/html", cacheControl: "no-store", body: "shell"},
		{name: "javascript asset", path: "/assets/index-abc.js", status: http.StatusOK, contentType: "text/javascript", cacheControl: "public, max-age=31536000, immutable", body: "ready"},
		{name: "stylesheet asset", path: "/assets/index-abc.css", status: http.StatusOK, contentType: "text/css", cacheControl: "public, max-age=31536000, immutable", body: "green"},
		{name: "stale javascript asset", path: "/assets/index-old.js", status: http.StatusNotFound},
		{name: "missing extensionless asset", path: "/assets/chunk", status: http.StatusNotFound},
		{name: "missing favicon", path: "/favicon.ico", status: http.StatusNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d; body = %q", response.Code, test.status, response.Body.String())
			}
			if test.contentType != "" && !strings.HasPrefix(response.Header().Get("Content-Type"), test.contentType) {
				t.Fatalf("Content-Type = %q, want prefix %q", response.Header().Get("Content-Type"), test.contentType)
			}
			if response.Header().Get("Cache-Control") != test.cacheControl {
				t.Fatalf("Cache-Control = %q, want %q", response.Header().Get("Cache-Control"), test.cacheControl)
			}
			if test.body != "" && !strings.Contains(response.Body.String(), test.body) {
				t.Fatalf("body = %q, want substring %q", response.Body.String(), test.body)
			}
			if test.status == http.StatusNotFound && strings.Contains(response.Body.String(), "shell") {
				t.Fatalf("missing asset unexpectedly received the SPA shell: %q", response.Body.String())
			}
		})
	}
}
