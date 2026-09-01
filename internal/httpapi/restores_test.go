package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/backup/application"
	backupdomain "github.com/zouyi/eco-guardian/internal/backup/domain"
	backupfs "github.com/zouyi/eco-guardian/internal/backup/filesystem"
	"github.com/zouyi/eco-guardian/internal/backup/ports"
	"github.com/zouyi/eco-guardian/internal/backup/restorejournal"
	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

type restoreHTTPTargets struct {
	projectID  domain.ID
	path       string
	generation string
	mu         sync.RWMutex
}

func (targets *restoreHTTPTargets) state() ports.RestoreTargetState {
	targets.mu.RLock()
	generation := targets.generation
	targets.mu.RUnlock()
	return ports.RestoreTargetState{ProjectID: targets.projectID, Mode: backupdomain.RestoreActive, CanonicalPath: targets.path, Identity: strings.Repeat("c", 64), Generation: generation, MaintenanceAvailable: true, RegistryState: "matched"}
}
func (targets *restoreHTTPTargets) setGeneration(generation string) {
	targets.mu.Lock()
	targets.generation = generation
	targets.mu.Unlock()
}
func (targets *restoreHTTPTargets) ResolveActive(context.Context, domain.ID) (ports.RestoreTargetState, error) {
	return targets.state(), nil
}
func (targets *restoreHTTPTargets) ResolveEmpty(context.Context, domain.ID, string) (ports.RestoreTargetState, error) {
	return ports.RestoreTargetState{}, errors.New("empty target unavailable")
}
func (targets *restoreHTTPTargets) Revalidate(_ context.Context, expected ports.RestoreTargetState) (ports.RestoreTargetState, error) {
	actual := targets.state()
	if expected.ProjectID != actual.ProjectID || expected.Mode != actual.Mode || expected.Identity != actual.Identity || expected.Generation != actual.Generation {
		return ports.RestoreTargetState{}, errors.New("target changed")
	}
	return actual, nil
}

type restoreHTTPMaintenance struct{}

func (restoreHTTPMaintenance) Acquire(context.Context, domain.ID, string, domain.ID) (ports.MaintenanceLease, error) {
	return nil, errors.New("maintenance intentionally unavailable in HTTP admission test")
}

type restoreHTTPReplacement struct{}

func (restoreHTTPReplacement) Stage(context.Context, string, string, domain.ID, domain.ID, int, int64, string) (ports.RestoreFileEvidence, error) {
	return ports.RestoreFileEvidence{}, errors.New("unused")
}
func (restoreHTTPReplacement) ParkOriginal(context.Context, string, domain.ID) (ports.RestoreFileEvidence, error) {
	return ports.RestoreFileEvidence{}, errors.New("unused")
}
func (restoreHTTPReplacement) Install(context.Context, string, domain.ID, ports.RestoreFileEvidence) (ports.RestoreFileEvidence, error) {
	return ports.RestoreFileEvidence{}, errors.New("unused")
}
func (restoreHTTPReplacement) InstallEmpty(context.Context, string, domain.ID, ports.RestoreFileEvidence) (ports.RestoreFileEvidence, error) {
	return ports.RestoreFileEvidence{}, errors.New("unused")
}
func (restoreHTTPReplacement) DiscardStaged(context.Context, string, domain.ID, ports.RestoreFileEvidence) error {
	return errors.New("unused")
}
func (restoreHTTPReplacement) VerifyInstalled(context.Context, string, domain.ID, int, int64, string) (ports.RestoreFileEvidence, error) {
	return ports.RestoreFileEvidence{}, errors.New("unused")
}
func (restoreHTTPReplacement) Rollback(context.Context, string, domain.ID, ports.RestoreFileEvidence) error {
	return errors.New("unused")
}
func (restoreHTTPReplacement) Cleanup(context.Context, string, domain.ID, ports.RestoreFileEvidence) error {
	return errors.New("unused")
}

func TestRestoreHTTPUsesOpaquePreflightGenerationAndIdempotentJobAdmission(t *testing.T) {
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
	backups := application.NewService(store.BackupSource{Store: project, AppVersion: "test"}, artifacts, store.BackupVerifier{}, project, project, backupfs.Probe{})
	backupCommand := backupdomain.Command{CommandVersion: backupdomain.CommandVersion, ProjectID: project.ProjectID(), Purpose: backupdomain.Manual, ManualReason: "restore HTTP", Source: backupdomain.SourceIdentity{}}
	backupJob, _, err := backups.Submit(context.Background(), backupCommand, "restore-http-backup")
	if err != nil {
		t.Fatal(err)
	}
	backupResult, err := backups.ExecuteStored(context.Background(), backupJob.ID)
	if err != nil {
		t.Fatal(err)
	}
	targets := &restoreHTTPTargets{projectID: project.ProjectID(), path: filepath.Clean(t.TempDir()), generation: strings.Repeat("d", 64)}
	journal, err := restorejournal.New(filepath.Join(filepath.Clean(t.TempDir()), "journals"))
	if err != nil {
		t.Fatal(err)
	}
	restores := application.NewRestoreService(backups, targets, journal, restoreHTTPMaintenance{}, restoreHTTPReplacement{})
	engine := gin.New()
	NewRestoreHandler(func() *application.RestoreService { return restores }).Register(engine)

	request := func(uri, key string, body any) *httptest.ResponseRecorder {
		encoded, _ := json.Marshal(body)
		httpRequest := httptest.NewRequest(http.MethodPost, uri, bytes.NewReader(encoded))
		httpRequest.Header.Set("Content-Type", "application/json")
		if key != "" {
			httpRequest.Header.Set("Idempotency-Key", key)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httpRequest)
		return response
	}
	invalidAuthority := request("/api/v1/restore-preflights", "", map[string]any{"backup_id": backupResult.BackupID, "target_mode": "active", "path": "/tmp/attacker"})
	if invalidAuthority.Code != http.StatusBadRequest || strings.Contains(invalidAuthority.Body.String(), "/tmp/attacker") {
		t.Fatalf("path authority status=%d body=%s", invalidAuthority.Code, invalidAuthority.Body)
	}
	preflightResponse := request("/api/v1/restore-preflights", "", map[string]any{"backup_id": backupResult.BackupID, "target_mode": "active"})
	if preflightResponse.Code != http.StatusOK {
		t.Fatalf("preflight status=%d body=%s", preflightResponse.Code, preflightResponse.Body)
	}
	var preflight struct {
		Generation string `json:"generation"`
	}
	if err = json.Unmarshal(preflightResponse.Body.Bytes(), &preflight); err != nil || len(preflight.Generation) != 64 {
		t.Fatalf("preflight=%s err=%v", preflightResponse.Body, err)
	}
	stale := request("/api/v1/restores", "restore-http", map[string]any{"backup_id": backupResult.BackupID, "target_mode": "active", "preflight_generation": strings.Repeat("e", 64), "confirmation": "RESTORE"})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "RESTORE_PREFLIGHT_STALE") {
		t.Fatalf("stale status=%d body=%s", stale.Code, stale.Body)
	}
	body := map[string]any{"backup_id": backupResult.BackupID, "target_mode": "active", "preflight_generation": preflight.Generation, "confirmation": "RESTORE"}
	first := request("/api/v1/restores", "restore-http", body)
	if first.Code != http.StatusAccepted || first.Header().Get("Location") == "" || !strings.Contains(first.Body.String(), `"result_type":"restore"`) {
		t.Fatalf("first status=%d headers=%v body=%s", first.Code, first.Header(), first.Body)
	}
	replay := request("/api/v1/restores", "restore-http", body)
	if replay.Code != http.StatusAccepted || replay.Header().Get("Location") != first.Header().Get("Location") {
		t.Fatalf("replay status=%d headers=%v body=%s", replay.Code, replay.Header(), replay.Body)
	}

	// A fresh server-authoritative target generation changes the immutable
	// request hash, so reusing the prior key must conflict rather than aliasing.
	targets.setGeneration(strings.Repeat("f", 64))
	changedPreflightResponse := request("/api/v1/restore-preflights", "", map[string]any{"backup_id": backupResult.BackupID, "target_mode": "active"})
	var changedPreflight struct {
		Generation string `json:"generation"`
	}
	if changedPreflightResponse.Code != http.StatusOK || json.Unmarshal(changedPreflightResponse.Body.Bytes(), &changedPreflight) != nil {
		t.Fatalf("changed preflight status=%d body=%s", changedPreflightResponse.Code, changedPreflightResponse.Body)
	}
	body["preflight_generation"] = changedPreflight.Generation
	conflict := request("/api/v1/restores", "restore-http", body)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "RESTORE_IDEMPOTENCY_CONFLICT") {
		t.Fatalf("conflict status=%d body=%s", conflict.Code, conflict.Body)
	}
}
