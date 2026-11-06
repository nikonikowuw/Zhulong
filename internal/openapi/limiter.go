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

	// rateLimitedCode names this package's rejection. It is deliberately not the generic
	// TOO_MANY_REQUESTS: that code is also produced by clientlog and the IP rate limiter,
	// and the i18n catalog keys messages by code alone, so sharing it would let whichever
	// package registered last rewrite this endpoint's user-visible message.
	rateLimitedCode = "FACE_UPLOAD_CONCURRENCY_LIMITED"

	// anonymousTokenID is the bucket shared by callers whose identity is missing. The
	// auth middleware rejects those requests before they reach here, so this only keeps
	// the limiter total if it is ever mounted elsewhere.
	anonymousTokenID = "anonymous"
)

// UploadLimiter manages concurrency for face upload operations.
type UploadLimiter struct {
	globalSem chan struct{}
	tokenMu   sync.Mutex
	tokenSems map[string]*tokenSlot
	tokenCap  int
}

// tokenSlot is one token's semaphore plus the number of callers holding a reference to
// it. The map entry is dropped when the last reference goes away: an operator cycling
// through credentials (create, use, revoke) would otherwise leave one channel behind per
// token ever seen, for the lifetime of the process.
type tokenSlot struct {
	sem  chan struct{}
	refs int
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
		tokenSems: make(map[string]*tokenSlot),
		tokenCap:  tokenCap,
	}
}

// acquireToken reserves a slot for tokenID and returns the slot the caller must pass back
// to releaseToken. The boolean reports whether a slot was actually taken.
func (l *UploadLimiter) acquireToken(tokenID string) (*tokenSlot, bool) {
	l.tokenMu.Lock()
	defer l.tokenMu.Unlock()

	slot, ok := l.tokenSems[tokenID]
	if !ok {
		slot = &tokenSlot{sem: make(chan struct{}, l.tokenCap)}
		l.tokenSems[tokenID] = slot
	}
	slot.refs++

	select {
	case slot.sem <- struct{}{}:
		return slot, true
	default:
		l.dropRefLocked(tokenID, slot)
		return nil, false
	}
}

// releaseToken frees a previously acquired slot.
func (l *UploadLimiter) releaseToken(tokenID string, slot *tokenSlot) {
	<-slot.sem

	l.tokenMu.Lock()
	defer l.tokenMu.Unlock()
	l.dropRefLocked(tokenID, slot)
}

// dropRefLocked releases one reference and forgets the entry once nothing holds it.
// Callers must hold tokenMu.
func (l *UploadLimiter) dropRefLocked(tokenID string, slot *tokenSlot) {
	slot.refs--
	if slot.refs > 0 {
		return
	}
	// A concurrent acquire may have replaced the entry between the lookups; only remove
	// the one this reference actually belonged to.
	if current, ok := l.tokenSems[tokenID]; ok && current == slot {
		delete(l.tokenSems, tokenID)
	}
}

// Middleware returns a Gin middleware enforcing global and per-token concurrency limits.
func (l *UploadLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenID := anonymousTokenID
		if id, ok := apitoken.GetIdentity(c); ok && id.ID != "" {
			tokenID = id.ID
		}

		// 1. Non-blocking acquisition of global slot
		select {
		case l.globalSem <- struct{}{}:
		default:
			rejectUpload(c, "Global face upload concurrency limit reached")
			return
		}

		// 2. Non-blocking acquisition of per-token slot
		slot, ok := l.acquireToken(tokenID)
		if !ok {
			// Release global slot if token slot is saturated
			<-l.globalSem
			rejectUpload(c, "Face upload concurrency limit reached for this token")
			return
		}

		defer func() {
			l.releaseToken(tokenID, slot)
			<-l.globalSem
		}()

		c.Next()
	}
}

// rejectUpload answers 429 with the documented retry hint and stops the chain.
func rejectUpload(c *gin.Context, message string) {
	c.Header("Retry-After", "1")
	httputil.WriteError(c, apperr.RateLimited(rateLimitedCode, message, nil))
	c.Abort()
}
