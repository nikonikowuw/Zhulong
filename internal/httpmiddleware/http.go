package httpmiddleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

const requestIDKey = "zhulong.request_id"

// MaxAPIRequestBodyBytes bounds non-file API request bodies to 1 MiB.
const MaxAPIRequestBodyBytes int64 = 1 << 20

var fallbackRequestIDCounter atomic.Uint64

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

// Recovery converts panics from HTTP handlers into a sanitized API response.
//
// Observers are notified after the 500 response is written, which is the only
// point where c.Writer.Status() reliably reports 500. An audit layer cannot
// observe panics from its own c.Next() continuation: a panicking handler never
// advances gin's handler index, so the continuation is skipped entirely, and a
// deferred read would still see the default 200 status.
func Recovery(logger *zap.Logger, observers ...PanicObserver) gin.HandlerFunc {
	return gin.CustomRecovery(func(c *gin.Context, recovered any) {
		logger.Error("HTTP handler panic", zap.Any("panic", recovered), zap.Stack("stack"))
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil))
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
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), requestIDContextKey{}, requestID))
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
	requestID, _ := ctx.Value(requestIDContextKey{}).(string)
	return requestID
}

type requestIDContextKey struct{}

// AccessLog records API request metadata without buffering request or response bodies.
func AccessLog(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()

		status := c.Writer.Status()
		fields := []zap.Field{
			zap.String("requestId", GetRequestID(c)),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Duration("latency", time.Since(started)),
			zap.Int("bytes", c.Writer.Size()),
			zap.String("clientIP", c.ClientIP()),
		}
		switch {
		case status >= http.StatusInternalServerError:
			logger.Error("HTTP request", fields...)
		case status >= http.StatusBadRequest:
			logger.Warn("HTTP request", fields...)
		default:
			logger.Info("HTTP request", fields...)
		}
	}
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
