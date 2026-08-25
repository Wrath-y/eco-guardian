package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	aiapplication "github.com/zouyi/eco-guardian/internal/ai/application"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
	"github.com/zouyi/eco-guardian/internal/project"
	store "github.com/zouyi/eco-guardian/internal/storage/sqlite"
)

const maxAIDecisionBodyBytes = 64 << 10

type AIDecisionCommands interface {
	Accept(context.Context, aiapplication.AcceptCommand) (aiapplication.PatchDecision, bool, error)
	Discard(context.Context, aiapplication.DiscardCommand) (aiapplication.PatchDecision, bool, error)
}

type AIDecisionServiceProvider func() AIDecisionCommands

func AIDecisionServiceFromProjectManager(manager *project.Manager) AIDecisionServiceProvider {
	return func() AIDecisionCommands {
		handle, ok := manager.ActiveHandle()
		if !ok {
			return nil
		}
		provider, ok := handle.(interface{ Store() *store.Store })
		if !ok {
			return nil
		}
		return aiapplication.DecisionService{Repository: provider.Store()}
	}
}

type AIDecisionHandler struct{ services AIDecisionServiceProvider }

func NewAIDecisionHandler(services AIDecisionServiceProvider) *AIDecisionHandler {
	return &AIDecisionHandler{services: services}
}

func (handler *AIDecisionHandler) Register(engine *gin.Engine) {
	engine.POST("/api/v1/draft-patches/:id/accept", handler.accept)
	engine.POST("/api/v1/draft-patches/:id/discard", handler.discard)
}

func (handler *AIDecisionHandler) current(c *gin.Context) AIDecisionCommands {
	if !safeLoopbackOrigin(c.Request) {
		problem(c, http.StatusForbidden, "AI_INPUT_INVALID", "Request origin is not allowed")
		return nil
	}
	if handler.services == nil {
		problem(c, http.StatusNotFound, "PROJECT_NOT_OPEN", "No active project")
		return nil
	}
	service := handler.services()
	if service == nil {
		problem(c, http.StatusNotFound, "PROJECT_NOT_OPEN", "No active project")
	}
	return service
}

func (handler *AIDecisionHandler) accept(c *gin.Context) {
	service := handler.current(c)
	if service == nil {
		return
	}
	patchID, ok := aiPatchID(c)
	if !ok {
		return
	}
	key, ok := aiIdempotencyKey(c)
	if !ok {
		return
	}
	var request riskdto.AcceptDraftPatchRequest
	if !decodeStrictAIJSON(c, &request) || len(request.Targets) < 1 || len(request.Targets) > 200 {
		return
	}
	targets := make([]aiapplication.TargetPrecondition, len(request.Targets))
	for index, target := range request.Targets {
		targets[index] = aiapplication.TargetPrecondition{EntityID: aicontract.EntityID(target.EntityId.String()), ExpectedEntityVersion: int64(target.ExpectedEntityVersion)}
	}
	command := aiapplication.AcceptCommand{PatchID: patchID, PatchHash: aicontract.Hash(request.PatchHash), BaseRevisionID: domain.ID(request.BaseRevisionId.String()), Targets: targets, IdempotencyKey: key, Actor: aiapplication.LocalDecisionActor}
	decision, _, err := service.Accept(c.Request.Context(), command)
	if err != nil {
		writeAIDecisionError(c, err)
		return
	}
	c.JSON(http.StatusOK, riskdto.AcceptDraftPatchResult{
		PatchId: uuid.MustParse(string(patchID)), PatchHash: riskdto.Hash(request.PatchHash), Decision: aiDecisionDTO(decision),
		RevisionId: uuid.MustParse(string(decision.AcceptedRevisionID)), RevisionUrl: "/api/v1/revisions/" + string(decision.AcceptedRevisionID), Published: false,
	})
}

func (handler *AIDecisionHandler) discard(c *gin.Context) {
	service := handler.current(c)
	if service == nil {
		return
	}
	patchID, ok := aiPatchID(c)
	if !ok {
		return
	}
	key, ok := aiIdempotencyKey(c)
	if !ok {
		return
	}
	var request riskdto.DiscardDraftPatchRequest
	if !decodeStrictAIJSON(c, &request) {
		return
	}
	reason := ""
	if request.Reason != nil {
		reason = *request.Reason
	}
	decision, _, err := service.Discard(c.Request.Context(), aiapplication.DiscardCommand{PatchID: patchID, PatchHash: aicontract.Hash(request.PatchHash), Reason: reason, IdempotencyKey: key, Actor: aiapplication.LocalDecisionActor})
	if err != nil {
		writeAIDecisionError(c, err)
		return
	}
	c.JSON(http.StatusOK, riskdto.DiscardDraftPatchResult{PatchId: uuid.MustParse(string(patchID)), PatchHash: riskdto.Hash(request.PatchHash), Decision: aiDecisionDTO(decision)})
}

func aiPatchID(c *gin.Context) (aicontract.PatchID, bool) {
	id := aicontract.PatchID(c.Param("id"))
	if !id.Valid() || !domain.ID(id).Valid() {
		problem(c, http.StatusNotFound, "AI_PATCH_NOT_FOUND", "DraftPatch was not found")
		return "", false
	}
	return id, true
}

func aiIdempotencyKey(c *gin.Context) (string, bool) {
	key := c.GetHeader("Idempotency-Key")
	if key == "" || key != strings.TrimSpace(key) || len(key) > 256 {
		problem(c, http.StatusBadRequest, "AI_IDEMPOTENCY_REQUIRED", "A valid Idempotency-Key is required")
		return "", false
	}
	return key, true
}

func decodeStrictAIJSON(c *gin.Context, target any) bool {
	if !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "application/json") {
		problem(c, http.StatusUnsupportedMediaType, "AI_INPUT_INVALID", "Content-Type must be application/json")
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxAIDecisionBodyBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxAIDecisionBodyBytes || strictJSON(raw, target) != nil {
		problem(c, http.StatusBadRequest, "AI_INPUT_INVALID", "Invalid AI command")
		return false
	}
	return true
}

func safeLoopbackOrigin(request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	hostname := strings.ToLower(parsed.Hostname())
	return hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1"
}

func aiDecisionDTO(decision aiapplication.PatchDecision) riskdto.AIHumanDecisionProjection {
	value := riskdto.AIHumanDecisionProjection{Id: uuid.MustParse(string(decision.ID)), Kind: riskdto.AIHumanDecisionProjectionKind(decision.Kind), Actor: decision.Actor, RequestHash: riskdto.Hash(decision.RequestHash), ResultHash: riskdto.Hash(decision.ResultHash), DecidedAt: decision.CreatedAt}
	if decision.AcceptedRevisionID.Valid() {
		accepted := uuid.MustParse(string(decision.AcceptedRevisionID))
		value.AcceptedRevisionId = &accepted
	}
	return value
}

func writeAIDecisionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, aiapplication.ErrDecisionInvalid):
		problem(c, http.StatusBadRequest, "AI_INPUT_INVALID", "Invalid AI decision request")
	case errors.Is(err, aiapplication.ErrDecisionNotFound):
		problem(c, http.StatusNotFound, "AI_PATCH_NOT_FOUND", "DraftPatch was not found")
	case errors.Is(err, aiapplication.ErrDecisionNotAcceptable):
		problem(c, http.StatusConflict, "AI_PATCH_NOT_ACCEPTABLE", "DraftPatch is not acceptable")
	case errors.Is(err, aiapplication.ErrDecisionStale):
		problem(c, http.StatusConflict, "REVISION_CONFLICT", "DraftPatch base or target revision conflicts")
	case errors.Is(err, aiapplication.ErrDecisionConflict):
		problem(c, http.StatusConflict, "AI_DECISION_CONFLICT", "DraftPatch decision conflicts")
	case errors.Is(err, aiapplication.ErrDecisionCanceled):
		problem(c, http.StatusConflict, "AI_CANCELED", "AI generation was canceled")
	case errors.Is(err, aiapplication.ErrDecisionValidation):
		problem(c, http.StatusUnprocessableEntity, "AI_OUTPUT_INVALID", "DraftPatch failed server validation")
	default:
		problem(c, http.StatusInternalServerError, "STORAGE_FAILURE", "AI decision could not be stored")
	}
}
