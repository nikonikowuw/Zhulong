package clientlog

import (
	"errors"
	"io"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/logsafe"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// sourceWeb marks every event as originating in the browser, set by the server
// so a client cannot claim to be another source.
const sourceWeb = "web"

// Handler accepts browser error batches.
type Handler struct {
	logger  *zap.Logger
	limiter *httpmiddleware.IPRateLimiter
}

// NewHandler constructs a client-log Handler. limiter may be nil, in which case
// only the schema and body limits apply.
func NewHandler(logger *zap.Logger, limiter *httpmiddleware.IPRateLimiter) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	// Browser events carry no meaningful server stack, and the global
	// ErrorLevel stacktrace policy would attach absolute build paths to every
	// client report. Raise the threshold above Fatal so these records never get
	// a stacktrace while the rest of the application keeps its policy.
	return &Handler{
		logger:  logger.Named("clientlog.handler").WithOptions(zap.AddStacktrace(zapcore.InvalidLevel)),
		limiter: limiter,
	}
}

// RegisterRoutes mounts the ingestion endpoint.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/client-logs")
	if h.limiter != nil {
		group.Use(httpmiddleware.RateLimit(h.limiter, h.logger))
	}
	group.POST("", h.Ingest)
}

// Ingest godoc
// @Summary      Ingest browser error events
// @Tags         clientlog
// @Accept       json
// @Produce      json
// @Param        payload body object true "Client error batch"
// @Success      200 {object} httputil.Response
// @Failure      413 {object} httputil.Response "Body exceeds 64 KiB"
// @Failure      422 {object} httputil.Response "Schema validation failed"
// @Failure      429 {object} httputil.Response "Rate limit exceeded"
// @Router       /client-logs [post]
func (h *Handler) Ingest(c *gin.Context) {
	// Bound the read even though MaxBodyLimit already wrapped the chain: this
	// handler is public, so it must not depend on an upstream middleware.
	reader := io.LimitReader(c.Request.Body, MaxRequestBodyBytes+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_FORMAT", "Invalid request format", err))
		return
	}
	if int64(len(body)) > MaxRequestBodyBytes {
		httputil.WritePayloadTooLarge(c)
		return
	}

	entries, dropped, err := validate(body)
	if err != nil {
		var validation *validationError
		if errors.As(err, &validation) {
			httputil.WriteError(c, httputil.NewValidationError("Validation failed", []httputil.FieldDetail{
				{Field: validation.field, Code: validation.code},
			}))
			return
		}
		httputil.WriteError(c, httputil.NewValidationError("Validation failed", nil))
		return
	}

	// The client address is masked before it can reach a log. ClientIP reports
	// the TCP peer because the router disables trusted proxies.
	rawClientIP := c.ClientIP()
	if rawClientIP == "" {
		// ClientIP() reports "" when the request context carries no remote
		// address; fall back to the raw socket so a misconfiguration is visible
		// as a masked network rather than a silent redaction marker.
		rawClientIP = c.Request.RemoteAddr
		if host, _, err := net.SplitHostPort(rawClientIP); err == nil {
			rawClientIP = host
		}
	}
	clientIP := logsafe.IP(rawClientIP)
	requestID := httpmiddleware.GetRequestID(c)

	for _, accepted := range entries {
		fields := make([]zap.Field, 0, len(accepted.Fields)+4)
		fields = append(fields,
			zap.String("source", sourceWeb),
			zap.String("clientIp", clientIP),
		)
		if accepted.Route != "" {
			fields = append(fields, zap.String("routeCategory", accepted.Route))
		}
		if requestID != "" {
			fields = append(fields, zap.String("requestId", requestID))
		}
		for _, field := range accepted.Fields {
			fields = append(fields, zap.String(field.Key, logsafe.Safe(field.Value)))
		}

		event := accepted.Msg
		if accepted.Level == levelWarn {
			h.logger.Warn(event, fields...)
			continue
		}
		h.logger.Error(event, fields...)
	}

	// The client-reported drop count is an aggregate signal, logged once per
	// batch and never allowed to bypass the entry or request limits.
	if dropped != nil && *dropped > 0 {
		h.logger.Warn("client.batch.dropped",
			zap.String("source", sourceWeb),
			zap.String("clientIp", clientIP),
			zap.Uint64("dropped", *dropped),
			zap.Int("accepted", len(entries)),
			zap.String("requestId", requestID),
		)
	}

	httputil.Success(c, nil)
}
