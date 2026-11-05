package apitoken

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler manages HTTP endpoints for API token creation, listing, and revocation.
type Handler struct {
	service *Service
	logger  *zap.Logger
}

// NewHandler creates a new Handler instance.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service: service,
		logger:  logger.Named("apitoken.handler"),
	}
}

// RegisterRoutes registers endpoints to the Gin router group (intended for protected group).
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/api-tokens")
	group.GET("", h.List)
	group.POST("", h.Create)
	group.DELETE("/:id", h.Revoke)
}

// List returns active tokens (or all tokens if includeRevoked=true).
// @Summary      List API tokens
// @Description  List active API tokens (and optionally revoked tokens).
// @Tags         apitoken
// @Produce      json
// @Param        includeRevoked query bool false "Include revoked tokens"
// @Success      200 {object} httputil.Response{data=APITokenListResponse}
// @Router       /api-tokens [get]
func (h *Handler) List(c *gin.Context) {
	includeRevoked := c.Query("includeRevoked") == "true"
	tokens, err := h.service.ListTokens(c.Request.Context(), includeRevoked)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	items := make([]APITokenInfo, 0, len(tokens))
	for i := range tokens {
		items = append(items, newAPITokenInfo(&tokens[i]))
	}

	httputil.Success(c, APITokenListResponse{Items: items})
}

// Create generates a new API token.
// @Summary      Create API token
// @Description  Create a new API token. The plaintext token is returned only once.
// @Tags         apitoken
// @Accept       json
// @Produce      json
// @Param        request body CreateAPITokenRequest true "Token creation request"
// @Success      201 {object} httputil.Response{data=APITokenCreateResponse}
// @Failure      409 {object} httputil.Response
// @Failure      422 {object} httputil.Response
// @Router       /api-tokens [post]
func (h *Handler) Create(c *gin.Context) {
	var req CreateAPITokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	token, rawToken, err := h.service.CreateToken(c.Request.Context(), req.Name)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	audithttp.Tag(c, ActionTokenCreate, token.Name, fmt.Sprintf("id=%s", token.ID))
	c.Header("Cache-Control", "no-store")

	httputil.SuccessWithStatus(c, http.StatusCreated, APITokenCreateResponse{
		APITokenInfo: newAPITokenInfo(token),
		Token:        rawToken,
	})
}

// Revoke soft-deletes an API token.
// @Summary      Revoke API token
// @Description  Soft-delete an API token by ID. Revoked tokens immediately stop working.
// @Tags         apitoken
// @Produce      json
// @Param        id path string true "API token ID"
// @Success      200 {object} httputil.Response{data=APITokenRevokeResponse}
// @Failure      404 {object} httputil.Response
// @Router       /api-tokens/{id} [delete]
func (h *Handler) Revoke(c *gin.Context) {
	id := c.Param("id")
	if err := h.service.RevokeToken(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}

	audithttp.Tag(c, ActionTokenRevoke, id, "")
	httputil.Success(c, APITokenRevokeResponse{Revoked: true})
}
