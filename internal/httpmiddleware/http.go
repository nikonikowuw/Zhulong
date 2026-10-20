package httpmiddleware

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/logctx"
	"go.uber.org/zap"
)

const requestIDKey = "zhulong.request_id"
const accessLogRecoveryKey = "zhulong.httpmiddleware.access_log_recovery"

// MaxAPIRequestBodyBytes bounds non-file API request bodies to 1 MiB.
const MaxAPIRequestBodyBytes int64 = 1 << 20

var (
	fallbackRequestIDCounter atomic.Uint64
	streamIDCounter          atomic.Uint64
)

// PanicObserver is notified after Recovery has handled a handler panic and the
// sanitized 500 response has been written. Implementations must not panic and
// must not write to the response.
//
// The interface lives here, in the middleware layer, so this package keeps no
// dependency on any business module; the application wires in a concrete
// observer.
type PanicObserver interface {
	OnPanic(c *gin.Context)
}

// Recovery converts panics from HTTP handlers into sanitized API errors. The
// WriteError boundary owns the single Error event for every unexpected 5xx.
func Recovery(logger *zap.Logger, observers ...PanicObserver) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, recovered any) {
		cause := fmt.Errorf("HTTP handler panic (%T)", recovered)
		httputil.WriteErrorWithLogger(c,
			httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", cause),
			logger,
			zap.Any("panic", recovered),
			zap.Stack("stack"),
		)
		if value, exists := c.Get(accessLogRecoveryKey); exists {
			if complete, ok := value.(func()); ok {
				complete()
			}
		}
		for _, observer := range observers {
			if observer != nil {
				observer.OnPanic(c)
			}
		}
	})
}

// RequestID propagates a validated incoming request ID or generates one.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader("X-Request-ID")
		if !validRequestID(requestID) {
			requestID = newRequestID()
		}
		c.Set(requestIDKey, requestID)
		c.Request = c.Request.WithContext(logctx.WithRequestID(c.Request.Context(), requestID))
		c.Header("X-Request-ID", requestID)
		c.Next()
	}
}

// GetRequestID returns the request ID set by RequestID.
func GetRequestID(c *gin.Context) string {
	value, exists := c.Get(requestIDKey)
	if !exists {
		return ""
	}
	requestID, _ := value.(string)
	return requestID
}

// RequestIDFromContext returns the request ID propagated to a standard context.
func RequestIDFromContext(ctx context.Context) string {
	return logctx.RequestID(ctx)
}

// AccessLog records API request metadata without buffering request or response bodies.
func AccessLog(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		requestID := GetRequestID(c)
		route := c.FullPath()
		streamKind := classifyLongLivedRequest(route, c.Request.Header.Get("Upgrade"))
		trackedWriter := &trackingResponseWriter{ResponseWriter: c.Writer}
		c.Writer = trackedWriter
		baseFields := []zap.Field{
			zap.String("requestId", requestID),
			zap.String("method", c.Request.Method),
			zap.String("route", route),
		}
		var streamLogger *zap.Logger
		var streamFields []zap.Field
		if streamKind != "" {
			streamID := streamIDCounter.Add(1)
			streamContext := logctx.WithStreamID(context.Background(), strconv.FormatUint(streamID, 10))
			streamLogger = logctx.Logger(streamContext, logger)
			streamFields = []zap.Field{
				zap.String("method", c.Request.Method),
				zap.String("route", route),
			}
			startFields := append([]zap.Field{zap.String("requestId", requestID)}, streamFields...)
			streamLogger.Info("stream.started", startFields...)
		}

		completed := false
		complete := func() {
			if completed {
				return
			}
			completed = true
			status := trackedWriter.Status()
			if streamKind != "" {
				established := streamEstablished(streamKind, status, trackedWriter.Header().Get("Content-Type"), trackedWriter.hijacked)
				fields := append(streamFields,
					zap.Int("status", status),
					zap.Int("bytes", trackedWriter.Size()),
					zap.String("clientIP", c.ClientIP()),
					zap.Bool("established", established),
				)
				if established {
					fields = append(fields, zap.Int64("session_ms", time.Since(started).Milliseconds()))
					streamLogger.Info("stream.closed", fields...)
					return
				}
				fields = append(fields, zap.Int64("latency_ms", time.Since(started).Milliseconds()), zap.String("requestId", requestID))
				streamLogger.Info("stream.closed", fields...)
				return
			}
			fields := append(baseFields,
				zap.Int("status", status),
				zap.Int("bytes", trackedWriter.Size()),
				zap.String("clientIP", c.ClientIP()),
				zap.Int64("latency_ms", time.Since(started).Milliseconds()),
			)
			logger.Info("HTTP request", fields...)
		}
		c.Set(accessLogRecoveryKey, complete)
		c.Next()
		complete()
	}
}

func classifyLongLivedRequest(route, upgrade string) string {
	if strings.EqualFold(strings.TrimSpace(upgrade), "websocket") || strings.HasSuffix(route, "/ws") {
		return "websocket"
	}
	if route == "/events" || strings.HasSuffix(route, "/cameras/events") {
		return "sse"
	}
	return ""
}

func streamEstablished(kind string, status int, contentType string, hijacked bool) bool {
	if kind == "websocket" {
		return status == http.StatusSwitchingProtocols || hijacked
	}
	return status >= http.StatusOK && status < http.StatusMultipleChoices && strings.HasPrefix(strings.ToLower(contentType), "text/event-stream")
}

type trackingResponseWriter struct {
	gin.ResponseWriter
	hijacked bool
}

func (w *trackingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, rw, err
}

func (w *trackingResponseWriter) Status() int {
	if w.hijacked {
		return http.StatusSwitchingProtocols
	}
	return w.ResponseWriter.Status()
}

func (w *trackingResponseWriter) Written() bool {
	return w.hijacked || w.ResponseWriter.Written()
}

// MaxBodyLimit rejects oversized API requests and caps bodies without a known length.
func MaxBodyLimit() gin.HandlerFunc {
	return MaxBodyLimitWithLimit(MaxAPIRequestBodyBytes)
}

// MaxBodyLimitWithLimit rejects oversized API requests and caps bodies at limit.
func MaxBodyLimitWithLimit(limit int64) gin.HandlerFunc {
	return MaxBodyLimitWithLimitAndCode(limit, "PAYLOAD_TOO_LARGE")
}

// MaxBodyLimitWithLimitAndCode applies a body limit with a caller-selected API error code.
func MaxBodyLimitWithLimitAndCode(limit int64, errorCode string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > limit {
			httputil.WriteError(c, httputil.NewError(http.StatusRequestEntityTooLarge, errorCode, "Request body too large", nil))
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

func validRequestID(requestID string) bool {
	if len(requestID) == 0 || len(requestID) > 128 {
		return false
	}
	for _, char := range requestID {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || strings.ContainsRune("-_.", char)) {
			return false
		}
	}
	return true
}

func newRequestID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return hex.EncodeToString(bytes[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 16) + "-" + strconv.FormatUint(fallbackRequestIDCounter.Add(1), 16)
}
