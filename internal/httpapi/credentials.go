package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	aiprovider "github.com/zouyi/eco-guardian/internal/ai/provider"
	"github.com/zouyi/eco-guardian/internal/httpapi/riskdto"
)

const maxCredentialRequestBytes = aiprovider.MaxCredentialBytes*6 + 128

type CredentialHandler struct {
	resolver aiprovider.CredentialResolver
}

func NewCredentialHandler(resolver aiprovider.CredentialResolver) *CredentialHandler {
	return &CredentialHandler{resolver: resolver}
}

func (h *CredentialHandler) Register(router *gin.Engine) {
	router.PUT("/api/v1/settings/credentials/:provider", h.put)
	router.DELETE("/api/v1/settings/credentials/:provider", h.delete)
}

func (h *CredentialHandler) put(c *gin.Context) {
	if !safeLoopbackOrigin(c.Request) {
		problem(c, http.StatusForbidden, "AI_CREDENTIAL_INVALID", "Request origin is not allowed")
		return
	}
	if !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "application/json") {
		problem(c, http.StatusUnsupportedMediaType, "AI_CREDENTIAL_INVALID", "Content-Type must be application/json")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxCredentialRequestBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxCredentialRequestBytes {
		problem(c, http.StatusBadRequest, "AI_CREDENTIAL_INVALID", "AI Provider credential is invalid")
		return
	}
	var request riskdto.PutProviderCredentialRequest
	if err := strictJSON(raw, &request); err != nil || request.Credential == nil {
		problem(c, http.StatusBadRequest, "AI_CREDENTIAL_INVALID", "AI Provider credential is invalid")
		return
	}
	value := []byte(*request.Credential)
	defer clear(value)
	if err := h.resolver.Put(c.Request.Context(), c.Param("provider"), value); err != nil {
		h.writeError(c, err)
		return
	}
	h.writeStatus(c)
}

func (h *CredentialHandler) delete(c *gin.Context) {
	if !safeLoopbackOrigin(c.Request) {
		problem(c, http.StatusForbidden, "AI_CREDENTIAL_INVALID", "Request origin is not allowed")
		return
	}
	if err := h.resolver.Delete(c.Request.Context(), c.Param("provider")); err != nil {
		h.writeError(c, err)
		return
	}
	h.writeStatus(c)
}

func (h *CredentialHandler) writeStatus(c *gin.Context) {
	providerName := c.Param("provider")
	secret, err := h.resolver.Resolve(c.Request.Context(), providerName)
	if err != nil && !errors.Is(err, aiprovider.ErrCredentialNotFound) {
		h.writeError(c, err)
		return
	}
	response := riskdto.ProviderCredentialStatus{
		Provider:          riskdto.ProviderCredentialStatusProvider(providerName),
		CredentialPresent: err == nil && secret.Present(),
	}
	if err == nil {
		source := riskdto.CredentialSource(secret.Source())
		response.Source = &source
	}
	c.JSON(http.StatusOK, response)
}

func (*CredentialHandler) writeError(c *gin.Context, err error) {
	if errors.Is(err, aiprovider.ErrCredentialInvalid) {
		problem(c, http.StatusBadRequest, "AI_CREDENTIAL_INVALID", "AI Provider credential is invalid")
		return
	}
	problem(c, http.StatusServiceUnavailable, "AI_CREDENTIAL_STORE_FAILED", "AI Provider credential store is unavailable")
}
