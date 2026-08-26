package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/zouyi/eco-guardian/internal/app/runtime/capability"
)

const runtimeActionCacheLimit = 256

type RuntimeActionService interface {
	Reprobe(context.Context) error
	Reconnect(context.Context) error
	RuntimeActionPreconditions() map[string]bool
}

type runtimeActionCall struct {
	done chan struct{}
	err  error
}

type RuntimeActionHandler struct {
	service RuntimeActionService
	actions *capability.ActionRegistry

	mu        sync.Mutex
	inflight  map[string]*runtimeActionCall
	completed map[string]error
	order     []string
}

func NewRuntimeActionHandler(service RuntimeActionService) *RuntimeActionHandler {
	return &RuntimeActionHandler{
		service: service, actions: capability.DefaultActionRegistry(),
		inflight: map[string]*runtimeActionCall{}, completed: map[string]error{}, order: []string{},
	}
}

func (handler *RuntimeActionHandler) Register(engine *gin.Engine) {
	engine.POST("/api/v1/runtime/reprobe", handler.invoke(capability.ActionRuntimeReprobe))
	engine.POST("/api/v1/runtime/reconnect", handler.invoke(capability.ActionGraphReconnect))
}

func (handler *RuntimeActionHandler) invoke(actionID string) gin.HandlerFunc {
	return func(ginContext *gin.Context) {
		if handler == nil || handler.service == nil {
			problem(ginContext, http.StatusServiceUnavailable, "RUNTIME_ACTION_UNAVAILABLE", "Runtime action is unavailable")
			return
		}
		key := ginContext.GetHeader("Idempotency-Key")
		if _, err := handler.actions.Resolve(actionID, handler.service.RuntimeActionPreconditions(), key); err != nil {
			switch {
			case errors.Is(err, capability.ErrIdempotencyKey):
				problem(ginContext, http.StatusBadRequest, "IDEMPOTENCY_KEY_INVALID", "A valid Idempotency-Key is required")
			case errors.Is(err, capability.ErrActionPrecondition):
				problem(ginContext, http.StatusConflict, "RUNTIME_ACTION_PRECONDITION_FAILED", "The runtime action precondition no longer holds")
			default:
				problem(ginContext, http.StatusBadRequest, "RUNTIME_ACTION_INVALID", "Runtime action is invalid")
			}
			return
		}
		err := handler.execute(ginContext.Request.Context(), actionID, key)
		if err != nil {
			problem(ginContext, http.StatusServiceUnavailable, "RUNTIME_ACTION_FAILED", "Runtime action could not complete")
			return
		}
		ginContext.Status(http.StatusNoContent)
	}
}

func (handler *RuntimeActionHandler) execute(ctx context.Context, actionID, idempotencyKey string) error {
	cacheKey := actionID + "\x00" + idempotencyKey
	handler.mu.Lock()
	if err, exists := handler.completed[cacheKey]; exists {
		handler.mu.Unlock()
		return err
	}
	if call, exists := handler.inflight[cacheKey]; exists {
		handler.mu.Unlock()
		select {
		case <-call.done:
			return call.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	call := &runtimeActionCall{done: make(chan struct{})}
	handler.inflight[cacheKey] = call
	handler.mu.Unlock()

	switch actionID {
	case capability.ActionRuntimeReprobe:
		call.err = handler.service.Reprobe(ctx)
	case capability.ActionGraphReconnect:
		call.err = handler.service.Reconnect(ctx)
	default:
		call.err = capability.ErrActionInvalid
	}

	handler.mu.Lock()
	delete(handler.inflight, cacheKey)
	handler.completed[cacheKey] = call.err
	handler.order = append(handler.order, cacheKey)
	if len(handler.order) > runtimeActionCacheLimit {
		delete(handler.completed, handler.order[0])
		handler.order = handler.order[1:]
	}
	close(call.done)
	handler.mu.Unlock()
	return call.err
}
