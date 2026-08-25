package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	aiapplication "github.com/zouyi/eco-guardian/internal/ai/application"
	aicontract "github.com/zouyi/eco-guardian/internal/ai/contract"
	"github.com/zouyi/eco-guardian/internal/domain"
)

type aiDecisionServiceFake struct {
	acceptCommand  aiapplication.AcceptCommand
	discardCommand aiapplication.DiscardCommand
	decision       aiapplication.PatchDecision
	err            error
}

func (fake *aiDecisionServiceFake) Accept(_ context.Context, command aiapplication.AcceptCommand) (aiapplication.PatchDecision, bool, error) {
	fake.acceptCommand = command
	return fake.decision, false, fake.err
}

func (fake *aiDecisionServiceFake) Discard(_ context.Context, command aiapplication.DiscardCommand) (aiapplication.PatchDecision, bool, error) {
	fake.discardCommand = command
	return fake.decision, false, fake.err
}

func aiHTTPID(suffix string) domain.ID {
	return domain.ID("018f9e40-0000-7000-8000-000000000" + suffix)
}
func aiHTTPHash(value string) string { return strings.Repeat(value, 64) }

func aiHTTPDecision(t *testing.T, kind aicontract.HumanDecisionKind, patchID aicontract.PatchID, requestHash aicontract.Hash, revisionID domain.ID) aiapplication.PatchDecision {
	t.Helper()
	decision, err := aiapplication.NewPatchDecision(aicontract.DecisionID(aiHTTPID("402")), patchID, kind, aiapplication.LocalDecisionActor, requestHash, revisionID, "", time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func aiDecisionEngine(service AIDecisionCommands) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	NewAIDecisionHandler(func() AIDecisionCommands { return service }).Register(engine)
	return engine
}

func TestAIDecisionHandlersUseStrictApplicationCommandsAndSafeResults(t *testing.T) {
	patchID := aicontract.PatchID(aiHTTPID("401"))
	baseID := aiHTTPID("403")
	targetID := aiHTTPID("404")
	requestHash := aicontract.Hash(aiHTTPHash("a"))
	acceptedRevision := aiHTTPID("405")
	service := &aiDecisionServiceFake{decision: aiHTTPDecision(t, aicontract.DecisionAccepted, patchID, requestHash, acceptedRevision)}
	engine := aiDecisionEngine(service)
	body := `{"patch_hash":"` + aiHTTPHash("b") + `","base_revision_id":"` + string(baseID) + `","targets":[{"entity_id":"` + string(targetID) + `","expected_entity_version":7}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/draft-patches/"+string(patchID)+"/accept", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "accept-key")
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"published":false`) || !strings.Contains(response.Body.String(), `"revision_id":"`+string(acceptedRevision)+`"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.acceptCommand.PatchID != patchID || service.acceptCommand.BaseRevisionID != baseID || service.acceptCommand.IdempotencyKey != "accept-key" || service.acceptCommand.Actor != aiapplication.LocalDecisionActor || len(service.acceptCommand.Targets) != 1 || service.acceptCommand.Targets[0].ExpectedEntityVersion != 7 {
		t.Fatalf("command=%#v", service.acceptCommand)
	}

	discardService := &aiDecisionServiceFake{decision: aiHTTPDecision(t, aicontract.DecisionDiscarded, patchID, requestHash, "")}
	discard := httptest.NewRequest(http.MethodPost, "/api/v1/draft-patches/"+string(patchID)+"/discard", strings.NewReader(`{"patch_hash":"`+aiHTTPHash("b")+`","reason":"Not suitable."}`))
	discard.Header.Set("Content-Type", "application/json")
	discard.Header.Set("Idempotency-Key", "discard-key")
	discardResponse := httptest.NewRecorder()
	aiDecisionEngine(discardService).ServeHTTP(discardResponse, discard)
	if discardResponse.Code != http.StatusOK || discardService.discardCommand.Reason != "Not suitable." || discardService.discardCommand.IdempotencyKey != "discard-key" {
		t.Fatalf("status=%d body=%s command=%#v", discardResponse.Code, discardResponse.Body.String(), discardService.discardCommand)
	}
}

func TestAIDecisionHandlersMapRevisionConflictAndRejectUnsafeRequests(t *testing.T) {
	patchID := aicontract.PatchID(aiHTTPID("411"))
	service := &aiDecisionServiceFake{err: aiapplication.ErrDecisionStale}
	body := `{"patch_hash":"` + aiHTTPHash("b") + `","base_revision_id":"` + string(aiHTTPID("412")) + `","targets":[{"entity_id":"` + string(aiHTTPID("413")) + `","expected_entity_version":1}]}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/draft-patches/"+string(patchID)+"/accept", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "stale-key")
	response := httptest.NewRecorder()
	aiDecisionEngine(service).ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"REVISION_CONFLICT"`) || strings.Contains(response.Body.String(), service.err.Error()) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	for name, mutate := range map[string]func(*http.Request){
		"unknown member": func(request *http.Request) {
			request.Body = io.NopCloser(strings.NewReader(strings.TrimSuffix(body, "}") + `,"secret":"canary"}`))
		},
		"missing key":    func(request *http.Request) { request.Header.Del("Idempotency-Key") },
		"foreign origin": func(request *http.Request) { request.Header.Set("Origin", "https://evil.example") },
		"wrong media":    func(request *http.Request) { request.Header.Set("Content-Type", "text/plain") },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := httptest.NewRequest(http.MethodPost, "/api/v1/draft-patches/"+string(patchID)+"/accept", strings.NewReader(body))
			candidate.Header.Set("Content-Type", "application/json")
			candidate.Header.Set("Idempotency-Key", "key")
			mutate(candidate)
			result := httptest.NewRecorder()
			aiDecisionEngine(&aiDecisionServiceFake{err: errors.New("must not be exposed")}).ServeHTTP(result, candidate)
			if result.Code < 400 || result.Code >= 500 || strings.Contains(result.Body.String(), "canary") || strings.Contains(result.Body.String(), "must not be exposed") {
				t.Fatalf("status=%d body=%s", result.Code, result.Body.String())
			}
		})
	}
}
