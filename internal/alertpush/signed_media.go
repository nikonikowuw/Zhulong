package alertpush

import (
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/mediafile"
)

const (
	DefaultSignedMediaRateLimitPerMin = 120
	DefaultSignedMediaRateLimitBurst  = 8
)

// SignedMediaHandlerOptions configures SignedMediaHandler construction.
type SignedMediaHandlerOptions struct {
	Signer       *URLSigner
	MediaHandler *mediafile.Handler
	GalleryMedia MediaReader
	RatePerMin   int
	RateBurst    int
	Logger       *zap.Logger
}

// SignedMediaHandler serves unauthenticated but signature-verified media requests.
type SignedMediaHandler struct {
	signer       *URLSigner
	mediaHandler *mediafile.Handler
	galleryMedia MediaReader
	rateLimiter  *httpmiddleware.IPRateLimiter
	logger       *zap.Logger
}

// NewSignedMediaHandler constructs a SignedMediaHandler.
func NewSignedMediaHandler(
	signer *URLSigner,
	mediaHandler *mediafile.Handler,
	logger *zap.Logger,
) *SignedMediaHandler {
	return NewSignedMediaHandlerWithGallery(
		signer,
		mediaHandler,
		nil,
		logger,
	)
}

// NewSignedMediaHandlerWithGallery constructs a SignedMediaHandler that can also stream
// HMAC-verified face gallery reference photos without exposing faces/ on /media/content/*.
func NewSignedMediaHandlerWithGallery(
	signer *URLSigner,
	mediaHandler *mediafile.Handler,
	galleryMedia MediaReader,
	logger *zap.Logger,
) *SignedMediaHandler {
	return NewSignedMediaHandlerWithOptions(SignedMediaHandlerOptions{
		Signer:       signer,
		MediaHandler: mediaHandler,
		GalleryMedia: galleryMedia,
		Logger:       logger,
	})
}

// NewSignedMediaHandlerWithOptions constructs a SignedMediaHandler with configurable rate limits.
func NewSignedMediaHandlerWithOptions(opts SignedMediaHandlerOptions) *SignedMediaHandler {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	perMin := opts.RatePerMin
	if perMin <= 0 {
		perMin = DefaultSignedMediaRateLimitPerMin
	}
	burst := opts.RateBurst
	if burst <= 0 {
		burst = DefaultSignedMediaRateLimitBurst
	}
	limiter := httpmiddleware.NewIPRateLimiter(httpmiddleware.RateLimitConfig{
		PerMinute: perMin,
		Burst:     burst,
	})
	return &SignedMediaHandler{
		signer:       opts.Signer,
		mediaHandler: opts.MediaHandler,
		galleryMedia: opts.GalleryMedia,
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

// ServeSignedMedia handles signature verification and delegates byte streaming to mediafile
// (for snapshots) or the isolated gallery reader (for faces).
//
// @Summary      Serve signed media content
// @Description  Streams snapshot or face gallery reference bytes verified by HMAC signature without cookie authentication.
// @Tags         media
// @Produce      image/jpeg
// @Param        path  path      string  true  "Relative media path, e.g. snapshots/cam_01/2026-11-03/01J8Z.jpg or faces/person_face.jpg"
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
		httputil.WriteError(
			c,
			apperr.PermissionDenied(CodeMediaURLInvalid, "Missing signature or expiration parameters", nil),
		)
		return
	}

	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil {
		httputil.WriteError(
			c,
			apperr.PermissionDenied(CodeMediaURLInvalid, "Invalid expiration format", err),
		)
		return
	}

	if h.signer == nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "URL signer unavailable", nil))
		return
	}

	if err := h.signer.Verify(relPath, exp, sig); err != nil {
		if errors.Is(err, ErrURLExpired) {
			httputil.WriteError(
				c,
				apperr.PermissionDenied(CodeMediaURLExpired, "Signed media URL has expired", err),
			)
			return
		}
		httputil.WriteError(
			c,
			apperr.PermissionDenied(CodeMediaURLInvalid, "Signed media URL signature is invalid", err),
		)
		return
	}

	normPath := normalizeRelativePath(relPath)
	if isAllowedFaceSignedPath(normPath) {
		h.serveGalleryMedia(c, normPath)
		return
	}

	if h.mediaHandler == nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Media service unavailable", nil))
		return
	}

	h.mediaHandler.ServeContent(c, normPath)
}

func (h *SignedMediaHandler) serveGalleryMedia(c *gin.Context, relPath string) {
	if h.galleryMedia == nil {
		httputil.WriteError(c, apperr.Internal("INTERNAL_ERROR", "Gallery media reader unavailable", nil))
		return
	}

	ctx := c.Request.Context()
	if c.Request.Method != http.MethodHead {
		if presigner, ok := h.galleryMedia.(MediaPresigner); ok {
			redirectURL, handled, err := presigner.PresignMediaURL(ctx, relPath, 0)
			if err != nil {
				writeSignedGalleryError(c, err)
				return
			}
			if handled && redirectURL != "" {
				c.Header("Cache-Control", "no-store")
				c.Redirect(http.StatusFound, redirectURL)
				return
			}
		}
	}

	reader, info, err := h.galleryMedia.OpenContent(ctx, relPath)
	if err != nil {
		writeSignedGalleryError(c, err)
		return
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			h.logger.Debug("close signed gallery media stream", zap.Error(closeErr))
		}
	}()

	name := info.Name
	if name == "" {
		name = path.Base(relPath)
	}
	contentType := signedGalleryContentType(name, info.ContentType)
	c.Header("Content-Type", contentType)
	http.ServeContent(
		c.Writer,
		c.Request,
		name,
		info.ModifiedAt,
		reader,
	)
}

func signedGalleryContentType(name, fallback string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg":
		return imageMIMEJPEG
	case ".png":
		return imageMIMEPNG
	case ".webp":
		return imageMIMEWEBP
	}
	if fallback != "" {
		return fallback
	}
	return "application/octet-stream"
}

func writeSignedGalleryError(c *gin.Context, err error) {
	if filestore.IsNotFound(err) || errors.Is(err, filestore.ErrNotAFile) {
		httputil.WriteError(
			c,
			apperr.NotFound("MEDIA_ENTRY_NOT_FOUND", "Media entry not found", err),
		)
		return
	}
	var appErr *apperr.Error
	if errors.As(err, &appErr) && appErr.Code == "MEDIA_ROOT_UNAVAILABLE" {
		httputil.WriteError(
			c,
			httputil.NewError(
				http.StatusServiceUnavailable,
				appErr.Code,
				appErr.Message,
				appErr.Err,
			),
		)
		return
	}
	httputil.WriteError(c, err)
}
