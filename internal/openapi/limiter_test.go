package openapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apitoken"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newLimiterRouter mounts the limiter in front of a handler that stays busy long enough
// for concurrent requests to overlap.
func newLimiterRouter(limiter *UploadLimiter, hold time.Duration) *gin.Engine {
	router := gin.New()
	router.POST("/upload", func(c *gin.Context) {
		if tokenID := c.GetHeader("X-Token-ID"); tokenID != "" {
			apitoken.SetIdentity(c, apitoken.Identity{ID: tokenID, Name: "Token-" + tokenID})
		}
		c.Next()
	}, limiter.Middleware(), func(c *gin.Context) {
		time.Sleep(hold)
		c.Status(http.StatusOK)
	})
	return router
}

// fire runs n concurrent requests through router and counts the status codes.
func fire(router *gin.Engine, n int, tokenFor func(i int) string) (ok, limited int32, headers []http.Header) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	headers = make([]http.Header, 0, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/upload", nil)
			req.Header.Set("X-Token-ID", tokenFor(idx))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			switch rec.Code {
			case http.StatusOK:
				atomic.AddInt32(&ok, 1)
			case http.StatusTooManyRequests:
				atomic.AddInt32(&limited, 1)
				mu.Lock()
				headers = append(headers, rec.Header())
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	return ok, limited, headers
}

func TestUploadLimiter_GlobalAndTokenConcurrency(t *testing.T) {
	limiter := NewUploadLimiterWithCaps(2, 1)
	router := newLimiterRouter(limiter, 50*time.Millisecond)

	// 1. Two requests from the same token: the per-token cap is the binding one.
	ok, limited, headers := fire(router, 2, func(int) string { return "token-A" })
	if ok != 1 || limited != 1 {
		t.Fatalf("same token: ok=%d limited=%d, want 1/1", ok, limited)
	}
	for _, header := range headers {
		if got := header.Get("Retry-After"); got != "1" {
			t.Errorf("Retry-After = %q, want %q", got, "1")
		}
	}

	// 2. Three distinct tokens: the global cap of 2 is the binding one.
	ok, limited, _ = fire(router, 3, func(i int) string { return fmt.Sprintf("token-%d", i) })
	if ok != 2 || limited != 1 {
		t.Fatalf("distinct tokens: ok=%d limited=%d, want 2/1", ok, limited)
	}

	// 3. Slots are released once the handlers return.
	ok, limited, _ = fire(router, 1, func(int) string { return "token-A" })
	if ok != 1 || limited != 0 {
		t.Fatalf("after completion: ok=%d limited=%d, want 1/0", ok, limited)
	}
}

// TestUploadLimiter_RejectionCodeIsOwned pins the code this package answers with. The
// generic TOO_MANY_REQUESTS is produced by both clientlog and the IP rate limiter, and
// the i18n catalog keys messages by code alone — sharing it would let whichever package
// registered last rewrite this endpoint's message.
func TestUploadLimiter_RejectionCodeIsOwned(t *testing.T) {
	limiter := NewUploadLimiterWithCaps(1, 1)
	router := newLimiterRouter(limiter, 50*time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/upload", nil)
		req.Header.Set("X-Token-ID", "token-owner")
		router.ServeHTTP(httptest.NewRecorder(), req)
	}()

	// Give the first request time to take the only slot.
	time.Sleep(10 * time.Millisecond)

	req := httptest.NewRequest(http.MethodPost, "/upload", nil)
	req.Header.Set("X-Token-ID", "token-owner")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	wg.Wait()

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if code := decodeEnvelope(t, rec).Code; code != rateLimitedCode {
		t.Fatalf("code = %q, want %q", code, rateLimitedCode)
	}
}

// TestUploadLimiter_ForgetsIdleTokens guards the memory bound: an operator cycling
// through credentials must not leave one semaphore behind per token ever seen.
func TestUploadLimiter_ForgetsIdleTokens(t *testing.T) {
	limiter := NewUploadLimiterWithCaps(4, 1)
	router := newLimiterRouter(limiter, 0)

	const tokens = 50
	for i := 0; i < tokens; i++ {
		ok, limited, _ := fire(router, 1, func(int) string { return fmt.Sprintf("token-%d", i) })
		if ok != 1 || limited != 0 {
			t.Fatalf("token %d: ok=%d limited=%d, want 1/0", i, ok, limited)
		}
	}

	limiter.tokenMu.Lock()
	entries := len(limiter.tokenSems)
	limiter.tokenMu.Unlock()
	if entries != 0 {
		t.Fatalf("limiter retained %d idle token buckets, want 0", entries)
	}
}

// TestUploadLimiter_KeepsBucketWhileBusy makes sure the cleanup above does not drop a
// bucket a concurrent request still holds.
func TestUploadLimiter_KeepsBucketWhileBusy(t *testing.T) {
	limiter := NewUploadLimiterWithCaps(4, 1)
	router := newLimiterRouter(limiter, 50*time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/upload", nil)
		req.Header.Set("X-Token-ID", "token-busy")
		router.ServeHTTP(httptest.NewRecorder(), req)
	}()

	time.Sleep(10 * time.Millisecond)
	limiter.tokenMu.Lock()
	entries := len(limiter.tokenSems)
	limiter.tokenMu.Unlock()
	if entries != 1 {
		t.Fatalf("in-flight token buckets = %d, want 1", entries)
	}

	wg.Wait()
	limiter.tokenMu.Lock()
	defer limiter.tokenMu.Unlock()
	if entries := len(limiter.tokenSems); entries != 0 {
		t.Fatalf("after completion buckets = %d, want 0", entries)
	}
}
