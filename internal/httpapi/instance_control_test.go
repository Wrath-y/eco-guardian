package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
)

type instanceProjectManagerFake struct {
	info     project.ProjectInfo
	active   bool
	closeErr error
	closed   bool
}

func (manager *instanceProjectManagerFake) Current() (project.ProjectInfo, bool) {
	return manager.info, manager.active
}

func (manager *instanceProjectManagerFake) Close(context.Context) error {
	if manager.closeErr != nil {
		return manager.closeErr
	}
	manager.closed = true
	manager.active = false
	return nil
}

func TestInstanceControlRequiresSecretAndMatchingActiveProject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id, _ := domain.NewID()
	manager := &instanceProjectManagerFake{info: project.ProjectInfo{ID: id, Name: "fixture"}, active: true}
	shutdown := make(chan struct{})
	engine := gin.New()
	NewInstanceControlHandler(manager, "process-secret", func() { close(shutdown) }).Register(engine)
	body, _ := json.Marshal(map[string]string{"project_id": string(id)})

	unauthorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/internal/instances/close", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(unauthorized, request)
	if unauthorized.Code != http.StatusUnauthorized || manager.closed {
		t.Fatalf("unauthorized=%d closed=%v", unauthorized.Code, manager.closed)
	}

	accepted := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/internal/instances/close", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(project.InstanceTokenHeader, "process-secret")
	engine.ServeHTTP(accepted, request)
	if accepted.Code != http.StatusAccepted || !manager.closed {
		t.Fatalf("accepted=%d closed=%v body=%s", accepted.Code, manager.closed, accepted.Body.String())
	}
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Fatal("shutdown was not requested")
	}
}

func TestInstanceControlPreservesCloseGuardFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id, _ := domain.NewID()
	manager := &instanceProjectManagerFake{info: project.ProjectInfo{ID: id}, active: true, closeErr: project.ErrCloseBlocked}
	engine := gin.New()
	NewInstanceControlHandler(manager, "process-secret", nil).Register(engine)
	body, _ := json.Marshal(map[string]string{"project_id": string(id)})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/internal/instances/close", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(project.InstanceTokenHeader, "process-secret")
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || manager.closed {
		t.Fatalf("status=%d closed=%v body=%s", recorder.Code, manager.closed, recorder.Body.String())
	}
	var problem Problem
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil || problem.Code != "CLOSE_BLOCKED" || !errors.Is(manager.closeErr, project.ErrCloseBlocked) {
		t.Fatalf("problem=%#v err=%v", problem, err)
	}
}
