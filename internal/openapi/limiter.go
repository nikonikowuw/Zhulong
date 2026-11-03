package openapi

import (
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apitoken"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

const (
	// GlobalMaxFaceUploads is the system-wide concurrency cap for OpenAPI face uploads.
	GlobalMaxFaceUploads = 3
	// TokenMaxFaceUploads is the per-token concurrency cap for OpenAPI face uploads.
	TokenMaxFaceUploads = 1
)

// UploadLimiter manages concurrency for face upload operations.
type UploadLimiter struct {
	globalSem chan struct{}
	tokenMu   sync.Mutex
	tokenSems map[string]chan struct{}
	tokenCap  int
}

// NewUploadLimiter creates a new limiter with default concurrency limits.
func NewUploadLimiter() *UploadLimiter {
	return NewUploadLimiterWithCaps(GlobalMaxFaceUploads, TokenMaxFaceUploads)
}

// NewUploadLimiterWithCaps creates a limiter with custom caps (useful for testing).
func NewUploadLimiterWithCaps(globalCap, tokenCap int) *UploadLimiter {
	if globalCap <= 0 {
		globalCap = GlobalMaxFaceUploads
	}
	if tokenCap <= 0 {
		tokenCap = TokenMaxFaceUploads
	}
	return &UploadLimiter{
		globalSem: make(chan struct{}, globalCap),
		tokenSems: make(map[string]chan struct{}),
		tokenCap:  tokenCap,
	}
}

func (l *UploadLimiter) getTokenSem(tokenID string) chan struct{} {
	l.tokenMu.Lock()
	defer l.tokenMu.Unlock()

	sem, exists := l.tokenSems[tokenID]
	if !exists {
		sem = make(chan struct{}, l.tokenCap)
		l.tokenSems[tokenID] = sem
	}
	return sem
}

// Middleware returns a Gin middleware enforcing global and per-token concurrency limits.
func (l *UploadLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenID := "anonymous"
		if id, ok := apitoken.GetIdentity(c); ok && id.ID != "" {
			tokenID = id.ID
		}

		// 1. Non-blocking acquisition of global slot
		select {
		case l.globalSem <- struct{}{}:
		default:
			c.Header("Retry-After", "1")
			httputil.WriteError(c, apperr.RateLimited("TOO_MANY_REQUESTS", "Global face upload concurrency limit reached", nil))
			c.Abort()
			return
		}

		// 2. Non-blocking acquisition of per-token slot
		tokenSem := l.getTokenSem(tokenID)
		select {
		case tokenSem <- struct{}{}:
		default:
			// Release global slot if token slot is saturated
			<-l.globalSem
			c.Header("Retry-After", "1")
			httputil.WriteError(c, apperr.RateLimited("TOO_MANY_REQUESTS", "Face upload concurrency limit reached for this token", nil))
			c.Abort()
			return
		}

		defer func() {
			<-tokenSem
			<-l.globalSem
		}()

		c.Next()
	}
}
