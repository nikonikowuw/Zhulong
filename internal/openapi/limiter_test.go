package openapi

import (
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

func TestUploadLimiter_GlobalAndTokenConcurrency(t *testing.T) {
	// Create limiter with globalCap=2, tokenCap=1
	limiter := NewUploadLimiterWithCaps(2, 1)

	router := gin.New()
	router.POST("/upload", func(c *gin.Context) {
		tokID := c.GetHeader("X-Token-ID")
		if tokID != "" {
			apitoken.SetIdentity(c, apitoken.Identity{ID: tokID, Name: "Token-" + tokID})
		}
		c.Next()
	}, limiter.Middleware(), func(c *gin.Context) {
		// Simulate in-flight face extraction
		time.Sleep(50 * time.Millisecond)
		c.Status(http.StatusOK)
	})

	// 1. Same token concurrent 2 requests -> 1 ok, 1 rejected with 429
	var wg sync.WaitGroup
	var okCount, rateLimitCount int32

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/upload", nil)
			req.Header.Set("X-Token-ID", "token-A")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code == http.StatusOK {
				atomic.AddInt32(&okCount, 1)
			} else if w.Code == http.StatusTooManyRequests {
				atomic.AddInt32(&rateLimitCount, 1)
				if w.Header().Get("Retry-After") != "1" {
					t.Errorf("expected Retry-After: 1, got %q", w.Header().Get("Retry-After"))
				}
			}
		}()
	}
	wg.Wait()

	if okCount != 1 || rateLimitCount != 1 {
		t.Fatalf("expected 1 ok and 1 rate-limited for same token, got ok=%d, rate=%d", okCount, rateLimitCount)
	}

	// 2. Global capacity: 3 different tokens concurrent -> 2 ok, 1 rejected
	okCount = 0
	rateLimitCount = 0
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/upload", nil)
			req.Header.Set("X-Token-ID", "token-diff-"+string(rune('1'+idx)))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			if w.Code == http.StatusOK {
				atomic.AddInt32(&okCount, 1)
			} else if w.Code == http.StatusTooManyRequests {
				atomic.AddInt32(&rateLimitCount, 1)
			}
		}(i)
	}
	wg.Wait()

	if okCount != 2 || rateLimitCount != 1 {
		t.Fatalf("expected 2 ok and 1 global rate-limited, got ok=%d, rate=%d", okCount, rateLimitCount)
	}

	// 3. After completion, slots must be released
	req := httptest.NewRequest(http.MethodPost, "/upload", nil)
	req.Header.Set("X-Token-ID", "token-A")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected slot to be released, got %d", w.Code)
	}
}
