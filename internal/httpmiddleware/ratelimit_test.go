package httpmiddleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// fakeClock is an injectable, race-safe clock for deterministic refill tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// admit is a convenience for budget-focused tests: it reserves and immediately
// releases the concurrency slot so the in-flight cap does not interfere.
func admit(l *IPRateLimiter, key string) admission {
	result := l.reserve(key)
	l.release(0)
	return result
}

func TestIPRateLimiterRefillRate(t *testing.T) {
	clock := newFakeClock()
	limiter := NewIPRateLimiter(RateLimitConfig{
		PerMinute: 60, Burst: 10, MaxKeys: 16, Now: clock.Now,
	})

	// Drain the burst.
	for i := 0; i < 10; i++ {
		if !admit(limiter, "1.2.3.4").allowed {
			t.Fatalf("burst token %d rejected", i)
		}
	}
	if admit(limiter, "1.2.3.4").allowed {
		t.Fatal("expected rejection after burst exhaustion")
	}

	// 60/minute = one token per second. Half a second is not enough.
	clock.Advance(500 * time.Millisecond)
	if admit(limiter, "1.2.3.4").allowed {
		t.Fatal("expected rejection after 500ms; refill must not be fractional-per-request")
	}

	// One full second restores exactly one token.
	clock.Advance(500 * time.Millisecond)
	if !admit(limiter, "1.2.3.4").allowed {
		t.Fatal("expected one token after 1s")
	}
	if admit(limiter, "1.2.3.4").allowed {
		t.Fatal("expected the single refilled token to be consumed")
	}
}

func TestIPRateLimiterBurstExhaustionAndRecovery(t *testing.T) {
	clock := newFakeClock()
	limiter := NewIPRateLimiter(RateLimitConfig{
		PerMinute: 60, Burst: 3, MaxKeys: 16, Now: clock.Now,
	})

	for i := 0; i < 3; i++ {
		if !admit(limiter, "10.0.0.1").allowed {
			t.Fatalf("burst token %d rejected", i)
		}
	}
	if admit(limiter, "10.0.0.1").allowed {
		t.Fatal("expected exhaustion")
	}

	// A different key keeps its own independent bucket.
	if !admit(limiter, "10.0.0.2").allowed {
		t.Fatal("second key must have its own bucket")
	}

	clock.Advance(time.Minute)
	for i := 0; i < 3; i++ {
		if !admit(limiter, "10.0.0.1").allowed {
			t.Fatalf("token %d not restored after one minute", i)
		}
	}
}

func TestIPRateLimiterBoundedKeySpace(t *testing.T) {
	clock := newFakeClock()
	limiter := NewIPRateLimiter(RateLimitConfig{
		PerMinute: 60, Burst: 1, MaxKeys: 32, Now: clock.Now,
	})

	for i := 0; i < 10000; i++ {
		admit(limiter, fmt.Sprintf("10.0.%d.%d", i/256, i%256))
		if got := limiter.Len(); got > 32 {
			t.Fatalf("key space grew to %d, max 32", got)
		}
	}
}

// TestIPRateLimiterEvictsOldestNotNewest pins the failure mode that matters: at
// the key limit the limiter must evict the least-recently-used entry, not reject
// an unknown key. Rejecting unknown keys would let one source flood the map with
// distinct addresses and lock every legitimate client out — a DoS defence that
// becomes a DoS.
func TestIPRateLimiterEvictsOldestNotNewest(t *testing.T) {
	clock := newFakeClock()
	limiter := NewIPRateLimiter(RateLimitConfig{
		PerMinute: 60, Burst: 1, MaxKeys: 2, Now: clock.Now,
	})

	if !admit(limiter, "old").allowed {
		t.Fatal("first key rejected")
	}
	if !admit(limiter, "newer").allowed {
		t.Fatal("second key rejected")
	}

	// `old` is now least-recently-used. A brand-new key must still be admitted,
	// evicting `old` rather than being refused.
	if !admit(limiter, "brand-new").allowed {
		t.Fatal("a new key must be admitted by evicting LRU, not rejected")
	}
	if got := limiter.Len(); got != 2 {
		t.Fatalf("key space = %d, want 2", got)
	}

	// `old` was evicted, so it starts a fresh bucket and is admitted.
	// `newer` was moved to the back by the eviction, so it still has its bucket.
	// Verify `old` was the one evicted by confirming it gets a full new burst.
	if !admit(limiter, "old").allowed {
		t.Fatal("evicted key must be re-admitted as a fresh bucket")
	}
}

func TestIPRateLimiterRecencyOrder(t *testing.T) {
	clock := newFakeClock()
	limiter := NewIPRateLimiter(RateLimitConfig{
		PerMinute: 60, Burst: 5, MaxKeys: 2, Now: clock.Now,
	})

	admit(limiter, "a")
	admit(limiter, "b")
	// Touch `a` so `b` becomes least-recently-used.
	admit(limiter, "a")
	admit(limiter, "c") // evicts `b`

	limiter.mu.Lock()
	_, hasB := limiter.buckets["b"]
	_, hasA := limiter.buckets["a"]
	limiter.mu.Unlock()
	if hasB {
		t.Fatal("expected the least-recently-used key `b` to be evicted")
	}
	if !hasA {
		t.Fatal("recently used key `a` must survive eviction")
	}
}

func TestIPRateLimiterProcessRequestBudget(t *testing.T) {
	clock := newFakeClock()
	limiter := NewIPRateLimiter(RateLimitConfig{
		PerMinute: 100, Burst: 100,
		MaxKeys:                  1024,
		ProcessRequestsPerMinute: 2,
		ProcessMaxInFlight:       100,
		Now:                      clock.Now,
	})

	// Distinct IPs so the per-IP budget never triggers.
	for i := 0; i < 2; i++ {
		if r := limiter.reserve(fmt.Sprintf("10.1.0.%d", i)); !r.allowed {
			t.Fatalf("request %d rejected by process budget too early", i)
		}
	}
	r := limiter.reserve("10.1.0.99")
	if r.allowed {
		t.Fatal("expected process-wide request budget to reject")
	}
	if r.code != rateLimitCodeProcessRequests {
		t.Fatalf("code = %q, want %q", r.code, rateLimitCodeProcessRequests)
	}

	// Refill after a minute allows traffic again.
	clock.Advance(time.Minute)
	if r := limiter.reserve("10.1.0.98"); !r.allowed {
		t.Fatal("expected process budget refill after one minute")
	}
}

func TestIPRateLimiterProcessInFlightBudget(t *testing.T) {
	clock := newFakeClock()
	limiter := NewIPRateLimiter(RateLimitConfig{
		PerMinute: 100, Burst: 100, MaxKeys: 1024,
		ProcessRequestsPerMinute: 1000,
		ProcessMaxInFlight:       2,
		Now:                      clock.Now,
	})

	limiter.reserve("10.2.0.1")
	limiter.reserve("10.2.0.2")
	r := limiter.reserve("10.2.0.3")
	if r.allowed {
		t.Fatal("expected in-flight cap to reject a third concurrent request")
	}
	if r.code != rateLimitCodeProcessInFlight {
		t.Fatalf("code = %q, want %q", r.code, rateLimitCodeProcessInFlight)
	}

	// Releasing one slot admits the next request.
	limiter.release(0)
	if r := limiter.reserve("10.2.0.4"); !r.allowed {
		t.Fatal("expected admission after releasing an in-flight slot")
	}
}

func TestIPRateLimiterProcessByteBudget(t *testing.T) {
	clock := newFakeClock()
	limiter := NewIPRateLimiter(RateLimitConfig{
		PerMinute: 100, Burst: 100, MaxKeys: 1024,
		ProcessRequestsPerMinute: 1000,
		ProcessBytesPerMinute:    1000,
		ProcessMaxInFlight:       100,
		Now:                      clock.Now,
	})

	limiter.mu.Lock()
	start := limiter.byteTokens
	limiter.mu.Unlock()
	if start != 1000 {
		t.Fatalf("byte tokens = %v, want 1000", start)
	}

	limiter.reserve("10.3.0.1")
	limiter.release(600)
	limiter.mu.Lock()
	remaining := limiter.byteTokens
	limiter.mu.Unlock()
	if remaining != 400 {
		t.Fatalf("byte tokens = %v, want 400 after charging 600", remaining)
	}

	// Refill is bounded by capacity, so a long idle period cannot bank credit.
	clock.Advance(time.Hour)
	limiter.reserve("10.3.0.2")
	limiter.release(0)
	limiter.mu.Lock()
	capped := limiter.byteTokens
	limiter.mu.Unlock()
	if capped > 1000 {
		t.Fatalf("byte tokens = %v exceeded capacity 1000", capped)
	}
}

func TestIPRateLimiterConcurrentSafety(t *testing.T) {
	clock := newFakeClock()
	limiter := NewIPRateLimiter(RateLimitConfig{
		PerMinute: 6000, Burst: 100, MaxKeys: 64, Now: clock.Now,
	})

	var waitGroup sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for i := 0; i < 200; i++ {
				limiter.reserve(fmt.Sprintf("10.4.%d.%d", worker, i%40))
				if i%3 == 0 {
					limiter.release(64)
				}
				_ = limiter.Len()
				_ = limiter.InFlight()
			}
		}(worker)
	}
	waitGroup.Wait()

	if got := limiter.Len(); got > 64 {
		t.Fatalf("key space = %d, max 64", got)
	}
}

func TestRateLimitMiddlewareRejectsWithEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	clock := newFakeClock()
	limiter := NewIPRateLimiter(RateLimitConfig{
		PerMinute: 60, Burst: 1, MaxKeys: 8, Now: clock.Now,
	})

	router := gin.New()
	router.Use(RequestID())
	router.GET("/api/v1/client-logs", RateLimit(limiter, nil), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": "OK"})
	})

	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/v1/client-logs", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", first.Code)
	}

	second := httptest.NewRecorder()
	router.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/api/v1/client-logs", nil))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429", second.Code)
	}
	body := second.Body.String()
	if want := `"code":"TOO_MANY_REQUESTS"`; !contains(body, want) {
		t.Fatalf("response body %q missing %s", body, want)
	}
	// The envelope must not echo request details.
	if contains(body, "10.0.0.1") {
		t.Fatalf("response body leaked client address: %q", body)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
