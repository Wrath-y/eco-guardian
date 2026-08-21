package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/app"
	"github.com/zouyi/eco-guardian/internal/domain"
)

func TestDurableResolverFuncWithoutCallbacksDoesNotClaimJob(t *testing.T) {
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	resolver := DurableResolverFunc{}
	if value, found, err := resolver.GetJob(context.Background(), id); err != nil || found || value != nil {
		t.Fatalf("value=%v found=%v err=%v", value, found, err)
	}
	if events, err := resolver.ListJobEvents(context.Background(), id, 3); err != nil || events != nil {
		t.Fatalf("events=%v err=%v", events, err)
	}
}

func TestRegisteredDurableResolverUsesSharedGetCancelAndSSERoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id, err := domain.NewID()
	if err != nil {
		t.Fatal(err)
	}
	resolver := DurableResolverFunc{
		Get: func(context.Context, domain.ID) (map[string]any, bool, error) {
			return map[string]any{"id": id, "kind": "simulation", "status": "succeeded"}, true, nil
		},
		Cancel: func(context.Context, domain.ID) (map[string]any, bool, bool, error) {
			return map[string]any{"id": id, "kind": "simulation", "status": "canceled"}, true, true, nil
		},
		Events: func(context.Context, domain.ID, int64) ([]DurableJobEvent, error) {
			return []DurableJobEvent{{Ordinal: 2, Payload: map[string]any{"kind": "simulation", "ordinal": 2}}}, nil
		},
	}
	handler := NewVersionHandler(func() app.VersioningService { return &fakeVersionService{} })
	handler.resolvers = []DurableResolver{resolver}
	engine := gin.New()
	handler.Register(engine)
	get := httptest.NewRecorder()
	engine.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+string(id), nil))
	if get.Code != http.StatusOK || !strings.Contains(get.Body.String(), `"kind":"simulation"`) {
		t.Fatalf("get=%d body=%s", get.Code, get.Body.String())
	}
	cancel := httptest.NewRecorder()
	engine.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+string(id)+"/cancel", nil))
	if cancel.Code != http.StatusAccepted || !strings.Contains(cancel.Body.String(), `"status":"canceled"`) {
		t.Fatalf("cancel=%d body=%s", cancel.Code, cancel.Body.String())
	}
	stream := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+string(id)+"/events", nil)
	request.Header.Set("Last-Event-ID", "1")
	engine.ServeHTTP(stream, request)
	if stream.Code != http.StatusOK || !strings.Contains(stream.Body.String(), "id: 2") || !strings.Contains(stream.Body.String(), "event: terminal") {
		t.Fatalf("stream=%d body=%s", stream.Code, stream.Body.String())
	}
}
