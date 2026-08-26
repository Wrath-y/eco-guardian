package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	appruntime "github.com/zouyi/eco-guardian/internal/app/runtime"
	"github.com/zouyi/eco-guardian/internal/app/runtime/capability"
	"github.com/zouyi/eco-guardian/internal/buildinfo"
)

type fakeRuntimeStatusSource struct {
	snapshot appruntime.StatusResource
	calls    int
}

func (source *fakeRuntimeStatusSource) Snapshot() appruntime.StatusResource {
	source.calls++
	return source.snapshot
}

func TestRuntimeStatusHandlerReturnsGeneratedSafeDTOWithoutSideEffects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	assembler, err := appruntime.NewStatusAssembler(appruntime.StatusResourceInput{
		Lifecycle: appruntime.StatusSnapshot{Generation: 4, Phase: appruntime.PhaseDegraded, ListenerURL: "http://127.0.0.1:8123", UpdatedAt: now},
		Build:     buildinfo.Info{Version: "1.0.0", Build: "release", Commit: "abc", PackageMode: buildinfo.PackageComplete},
		Project:   appruntime.ProjectStatus{State: "none"}, Capabilities: []capability.Result{}, Recovery: []appruntime.RecoveryStatus{},
		LogLocation: "<local-app-data>/EcoGuardian/logs",
	})
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeRuntimeStatusSource{snapshot: assembler.Snapshot()}
	engine := gin.New()
	NewRuntimeStatusHandler(source).Register(engine)
	first := httptest.NewRecorder()
	second := httptest.NewRecorder()
	engine.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/v1/runtime/status", nil))
	engine.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/v1/runtime/status", nil))
	if first.Code != http.StatusOK || first.Body.String() != second.Body.String() || source.calls != 2 {
		t.Fatalf("status=%d calls=%d first=%s second=%s", first.Code, source.calls, first.Body.String(), second.Body.String())
	}
	if strings.Contains(first.Body.String(), "secret") || strings.Contains(first.Body.String(), "diagnostic_pid") || strings.Contains(first.Body.String(), "child output") {
		t.Fatalf("unsafe status body=%s", first.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil || body["schema_version"] != float64(1) || body["phase"] != "degraded" {
		t.Fatalf("body=%v err=%v", body, err)
	}
}

func TestRuntimeStatusHandlerUsesSafeProblemWhenSnapshotIsNotReady(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewRuntimeStatusHandler(&fakeRuntimeStatusSource{}).Register(engine)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/runtime/status", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Content-Type") != "application/problem+json" || !strings.Contains(response.Body.String(), "RUNTIME_STATUS_UNAVAILABLE") {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}
