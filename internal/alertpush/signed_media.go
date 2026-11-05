package alertpush

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/mediafile"
	"go.uber.org/zap"
)

// SignedMediaHandler serves unauthenticated but signature-verified media requests.
type SignedMediaHandler struct {
	signer       *URLSigner
	mediaHandler *mediafile.Handler
	rateLimiter  *httpmiddleware.IPRateLimiter
	logger       *zap.Logger
}

// NewSignedMediaHandler constructs a SignedMediaHandler.
func NewSignedMediaHandler(signer *URLSigner, mediaHandler *mediafile.Handler, logger *zap.Logger) *SignedMediaHandler {
	if logger == nil {
		logger = zap.NewNop()
	}
	// Limit per IP: 120 req/min, burst 8
	limiter := httpmiddleware.NewIPRateLimiter(httpmiddleware.RateLimitConfig{
		PerMinute: 120,
		Burst:     8,
	})
	return &SignedMediaHandler{
		signer:       signer,
		mediaHandler: mediaHandler,
		rateLimiter:  limiter,
		logger:       logger.Named("alertpush.signed_media"),
	}
}

// RegisterRoutes registers the public signed media endpoint.
func (h *SignedMediaHandler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/media")
	group.Use(httpmiddleware.RateLimit(h.rateLimiter, h.logger))
	{
		group.GET("/signed/*path", h.ServeSignedMedia)
		group.HEAD("/signed/*path", h.ServeSignedMedia)
	}
}

// ServeSignedMedia handles signature verification and delegates byte streaming to mediafile.
//
// @Summary      Serve signed media content
// @Description  Streams snapshot bytes verified by HMAC signature without cookie authentication.
// @Tags         media
// @Produce      image/jpeg
// @Param        path  path      string  true  "Relative media path, e.g. snapshots/cam_01/2026-11-03/01J8Z.jpg"
// @Param        exp   query     int64   true  "Expiration Unix timestamp"
// @Param        sig   query     string  true  "Base64URL HMAC signature"
// @Success      200   {file}    binary
// @Failure      403   {object}  httputil.Response
// @Failure      404   {object}  httputil.Response
// @Router       /media/signed/{path} [get]
func (h *SignedMediaHandler) ServeSignedMedia(c *gin.Context) {
	relPath := strings.TrimPrefix(c.Param("path"), "/")
	expStr := c.Query("exp")
	sig := c.Query("sig")

	if expStr == "" || sig == "" {
		httputil.WriteError(c, apperr.PermissionDenied(CodeMediaURLInvalid, "Missing signature or expiration parameters", nil))
		return
	}

	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		httputil.WriteError(c, apperr.PermissionDenied(CodeMediaURLInvalid, "Invalid expiration format", err))
		return
	}

	if h.signer == nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "URL signer unavailable", nil))
		return
	}

	if err := h.signer.Verify(relPath, exp, sig); err != nil {
		if errors.Is(err, ErrURLExpired) {
			httputil.WriteError(c, apperr.PermissionDenied(CodeMediaURLExpired, "Signed media URL has expired", err))
			return
		}
		httputil.WriteError(c, apperr.PermissionDenied(CodeMediaURLInvalid, "Signed media URL signature is invalid", err))
		return
	}

	if h.mediaHandler == nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Media service unavailable", nil))
		return
	}

	h.mediaHandler.ServeContent(c, relPath)
}
