package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

func backupHTTPFixture(t *testing.T) (*gin.Engine, *application.Service, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	registry, err := domain.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	project, _, err := store.Create(context.Background(), t.TempDir(), registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = project.Close() })
	artifacts, err := backupfs.NewStore(filepath.Clean(t.TempDir()), store.DBSchemaVersion())
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store.BackupSource{Store: project, AppVersion: "test"}, artifacts, store.BackupVerifier{}, project, project, backupfs.Probe{})
	engine := gin.New()
	provider := BackupServiceProvider(func() *application.Service { return service })
	NewBackupHandler(provider, func() (int, int) { return 10, 5 }).Register(engine)
	return engine, service, project
}

func TestBackupHTTPManualIdempotencyInventoryAndOpaqueInputs(t *testing.T) {
	engine, _, project := backupHTTPFixture(t)
	submit := func(reason string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"purpose": "manual", "reason": reason})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/backups", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "manual-http-key")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		return response
	}
	first := submit("before editing")
	if first.Code != http.StatusAccepted || first.Header().Get("Location") == "" {
		t.Fatalf("status=%d body=%s headers=%v", first.Code, first.Body, first.Header())
	}
	var accepted struct {
		Job struct {
			ID domain.ID `json:"id"`
		} `json:"job"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &accepted); err != nil || !accepted.Job.ID.Valid() {
		t.Fatalf("accepted=%s err=%v", first.Body, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, err := project.GetJob(context.Background(), accepted.Job.ID)
		if err == nil && job.Status.Terminal() {
			if job.Status != "succeeded" {
				t.Fatalf("job=%#v", job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("backup worker did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	replay := submit("before editing")
	if replay.Code != http.StatusAccepted {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body)
	}
	conflict := submit("changed input")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body)
	}
	list := httptest.NewRecorder()
	engine.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v1/backups?limit=50", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body)
	}
	var inventory struct {
		Items []struct {
			BackupID domain.ID `json:"backup_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &inventory); err != nil || len(inventory.Items) != 1 {
		t.Fatalf("inventory=%s err=%v", list.Body, err)
	}
	for _, limit := range []string{"0", "201", "not-a-number"} {
		invalidLimit := httptest.NewRecorder()
		engine.ServeHTTP(invalidLimit, httptest.NewRequest(http.MethodGet, "/api/v1/backups?limit="+limit, nil))
		if invalidLimit.Code != http.StatusBadRequest || !bytes.Contains(invalidLimit.Body.Bytes(), []byte(`"code":"VALIDATION_FAILED"`)) {
			t.Fatalf("limit=%s status=%d body=%s", limit, invalidLimit.Code, invalidLimit.Body)
		}
	}
	get := httptest.NewRecorder()
	engine.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/backups/"+string(inventory.Items[0].BackupID), nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body)
	}
	repeatedGet := httptest.NewRecorder()
	engine.ServeHTTP(repeatedGet, httptest.NewRequest(http.MethodGet, "/api/v1/backups/"+string(inventory.Items[0].BackupID), nil))
	if repeatedGet.Code != http.StatusOK || repeatedGet.Body.String() != get.Body.String() {
		t.Fatalf("repeated GET changed projection: first=%s second=%s", get.Body, repeatedGet.Body)
	}
	afterReads := httptest.NewRecorder()
	engine.ServeHTTP(afterReads, httptest.NewRequest(http.MethodGet, "/api/v1/backups?limit=200", nil))
	var afterInventory struct {
		Items []json.RawMessage `json:"items"`
	}
	if afterReads.Code != http.StatusOK || json.Unmarshal(afterReads.Body.Bytes(), &afterInventory) != nil || len(afterInventory.Items) != 1 {
		t.Fatalf("read-only requests changed inventory: status=%d body=%s", afterReads.Code, afterReads.Body)
	}
	bad := httptest.NewRecorder()
	engine.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/v1/backups/not-a-path", nil))
	if bad.Code != http.StatusNotFound {
		t.Fatalf("opaque ID status=%d body=%s", bad.Code, bad.Body)
	}
}
