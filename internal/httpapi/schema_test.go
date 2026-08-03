package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/domain"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

func TestSchemaHandlerServesAllBuiltIns(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, _ := domain.NewRegistry()
	engine := gin.New()
	NewSchemaHandler(r).Register(engine)
	for _, kind := range r.Kinds() {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/schemas/entities/"+string(kind), nil)
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", kind, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/schemas/entities/nope", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatal(w.Code)
	}
}

func TestEntityHandlerETagPreconditions(t *testing.T) {
	r, _ := domain.NewRegistry()
	s, _, err := store.Create(context.Background(), t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	engine := gin.New()
	NewEntityHandler(func() EntityStore { return s }).Register(engine)
	body := []byte(`{"key":"fire","name":"Fire","payload":{"category":"element","parent_tag_ids":[]}}`)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/entities/tag", bytes.NewReader(body)))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Entity domain.Entity `json:"entity"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/api/v1/entities/tag/"+string(response.Entity.ID), bytes.NewReader([]byte(`{"name":"Flame"}`))))
	if w.Code != 428 {
		t.Fatal(w.Code)
	}
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/entities/tag/"+string(response.Entity.ID), bytes.NewReader([]byte(`{"name":"Flame"}`)))
	req.Header.Set("If-Match", entityETag(response.Entity))
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != 200 || w.Header().Get("ETag") == entityETag(response.Entity) {
		t.Fatalf("patch=%d etag=%s", w.Code, w.Header().Get("ETag"))
	}
}

func TestEntityHandlerRequiresProjectAndBoundsPage(t *testing.T) {
	engine := gin.New()
	NewEntityHandler(func() EntityStore { return nil }).Register(engine)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/entities/tag", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	r, _ := domain.NewRegistry()
	s, _, err := store.Create(context.Background(), t.TempDir(), r)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	engine = gin.New()
	NewEntityHandler(func() EntityStore { return s }).Register(engine)
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/entities/tag?limit=201", nil))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
