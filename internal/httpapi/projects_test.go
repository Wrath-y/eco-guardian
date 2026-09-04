package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/project"
)

type projectNotificationLock struct{}

func (projectNotificationLock) Release() error { return nil }

type projectNotificationLocker struct{}

func (projectNotificationLocker) Acquire(string) (project.Lock, error) {
	return projectNotificationLock{}, nil
}

type projectNotificationHandle struct{ id domain.ID }

func (projectNotificationHandle) Close() error         { return nil }
func (handle projectNotificationHandle) ID() domain.ID { return handle.id }

type projectNotificationFactory struct{ handle project.ProjectHandle }

func (factory projectNotificationFactory) Create(context.Context, string) (project.ProjectHandle, error) {
	return factory.handle, nil
}

func (factory projectNotificationFactory) Open(context.Context, string) (project.ProjectHandle, error) {
	return factory.handle, nil
}

func TestProjectHandlerNotifiesRuntimeAfterSuccessfulStateChanges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	tokens := project.NewTokenStore(time.Minute, nil)
	token, _, err := tokens.Issue(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := project.NewManager(tokens, projectNotificationLocker{}, projectNotificationFactory{handle: projectNotificationHandle{id: id}}, project.NoJobs{}, nil)
	changes := []bool{}
	handler := NewProjectHandler(manager, nil).ObserveProjectStateChanges(func(active bool) {
		changes = append(changes, active)
	})
	engine := gin.New()
	handler.Register(engine)

	open := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"selection_token":"`+token+`","mode":"create"}`))
	open.Header.Set("Content-Type", "application/json")
	openResponse := httptest.NewRecorder()
	engine.ServeHTTP(openResponse, open)
	if openResponse.Code != http.StatusCreated {
		t.Fatalf("open status=%d body=%s", openResponse.Code, openResponse.Body.String())
	}

	closeResponse := httptest.NewRecorder()
	engine.ServeHTTP(closeResponse, httptest.NewRequest(http.MethodPost, "/api/v1/projects/close", nil))
	if closeResponse.Code != http.StatusNoContent {
		t.Fatalf("close status=%d body=%s", closeResponse.Code, closeResponse.Body.String())
	}
	if len(changes) != 2 || !changes[0] || changes[1] {
		t.Fatalf("state changes=%v", changes)
	}
}
