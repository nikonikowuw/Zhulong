package httpmiddleware

import (
	"container/list"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/logsafe"
	"go.uber.org/zap"
)

// Defaults for the client-log ingestion surface. The per-IP values are fixed by
// the API contract; the process-wide values are provisional defaults that a
// deployment profile may override. They bound resource use, but they are not a
// signed bandwidth or capacity budget for any target board.
const (
	DefaultRateLimitPerMinute    = 60
	DefaultRateLimitBurst        = 10
	DefaultRateLimitMaxKeys      = 1024
	DefaultProcessRequestsPerMin = 120
	DefaultProcessBytesPerMinute = 2 << 20
	DefaultProcessMaxInFlight    = 8
	rateLimitCodePerIP           = "TOO_MANY_REQUESTS"
	rateLimitCodeProcessRequests = "CLIENT_LOG_BUDGET_EXHAUSTED"
	rateLimitCodeProcessInFlight = "CLIENT_LOG_CONCURRENCY_LIMIT"
	rateLimitWindow              = time.Minute
	rateLimitKeyContextKey       = "zhulong.httpmiddleware.ratelimit_key"
)

// RateLimitConfig configures a RateLimit middleware. Zero values fall back to
// the package defaults, so a missing config section never aborts startup.
type RateLimitConfig struct {
	// PerMinute is the sustained per-IP request rate, refilled continuously.
	PerMinute int
	// Burst is the per-IP token bucket capacity.
	Burst int
	// MaxKeys bounds the tracked-key set. At the limit the least-recently-used
	// key is evicted, which keeps address churn from growing memory without
	// limit while still admitting new clients.
	MaxKeys int
	// ProcessRequestsPerMinute bounds aggregate requests across every source.
	ProcessRequestsPerMinute int
	// ProcessBytesPerMinute bounds aggregate request-body bytes.
	ProcessBytesPerMinute int
	// ProcessMaxInFlight bounds concurrently executing handlers.
	ProcessMaxInFlight int
	// Now defaults to time.Now. Tests inject a fake clock.
	Now func() time.Time
}

func (c RateLimitConfig) normalize() RateLimitConfig {
	if c.PerMinute <= 0 {
		c.PerMinute = DefaultRateLimitPerMinute
	}
	if c.Burst <= 0 {
		c.Burst = DefaultRateLimitBurst
	}
	if c.MaxKeys <= 0 {
		c.MaxKeys = DefaultRateLimitMaxKeys
	}
	if c.ProcessRequestsPerMinute <= 0 {
		c.ProcessRequestsPerMinute = DefaultProcessRequestsPerMin
	}
	if c.ProcessBytesPerMinute <= 0 {
		c.ProcessBytesPerMinute = DefaultProcessBytesPerMinute
	}
	if c.ProcessMaxInFlight <= 0 {
		c.ProcessMaxInFlight = DefaultProcessMaxInFlight
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// IPRateLimiter is a bounded, concurrency-safe token bucket keyed by client IP
// with an additional process-wide request, byte and concurrency budget.
//
// It is deliberately not lock-free: map and LRU bookkeeping share one short
// mutex. The critical section performs no I/O, so contention stays bounded.
type IPRateLimiter struct {
	config RateLimitConfig
	now    func() time.Time

	mu      sync.Mutex
	buckets map[string]*list.Element
	order   *list.List // front = most recently used

	processTokens float64
	processLast   time.Time
	byteTokens    float64
	byteLast      time.Time
	inFlight      int
}

type bucketEntry struct {
	key    string
	bucket tokenBucket
}

// NewIPRateLimiter constructs a limiter from config. Zero-valued fields fall
// back to the package defaults.
func NewIPRateLimiter(config RateLimitConfig) *IPRateLimiter {
	config = config.normalize()
	now := config.Now
	return &IPRateLimiter{
		config:        config,
		now:           now,
		buckets:       make(map[string]*list.Element, config.MaxKeys),
		order:         list.New(),
		processTokens: float64(config.ProcessRequestsPerMinute),
		processLast:   now(),
		byteTokens:    float64(config.ProcessBytesPerMinute),
		byteLast:      now(),
	}
}

// Len reports the number of tracked keys. It exists so tests can assert the
// bounded key space.
func (l *IPRateLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// InFlight reports the number of handlers currently admitted by the process-wide
// concurrency budget.
func (l *IPRateLimiter) InFlight() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.inFlight
}

// refill adds tokens accrued since last at the given rate per minute, capped at
// capacity. It must be called with l.mu held.
func refill(bucket *tokenBucket, now time.Time, perMinute float64, capacity float64) {
	elapsed := now.Sub(bucket.last)
	if elapsed <= 0 {
		bucket.last = now
		return
	}
	bucket.tokens += elapsed.Minutes() * perMinute
	if bucket.tokens > capacity {
		bucket.tokens = capacity
	}
	bucket.last = now
}

// allowIPLocked applies the per-IP budget. It must be called with l.mu held.
func (l *IPRateLimiter) allowIPLocked(key string, now time.Time) bool {
	capacity := float64(l.config.Burst)

	if element, ok := l.buckets[key]; ok {
		l.order.MoveToFront(element)
		entry := element.Value.(*bucketEntry)
		refill(&entry.bucket, now, float64(l.config.PerMinute), capacity)
		if entry.bucket.tokens < 1 {
			return false
		}
		entry.bucket.tokens--
		return true
	}

	// Bound the key space before inserting. Evicting the least-recently-used
	// entry rather than rejecting an unknown key is essential: rejecting new
	// keys would let one source exhaust the map and lock out every legitimate
	// client, turning a DoS defence into a DoS.
	for len(l.buckets) >= l.config.MaxKeys {
		oldest := l.order.Back()
		if oldest == nil {
			break
		}
		l.order.Remove(oldest)
		delete(l.buckets, oldest.Value.(*bucketEntry).key)
	}

	entry := &bucketEntry{key: key, bucket: tokenBucket{tokens: capacity, last: now}}
	element := l.order.PushFront(entry)
	l.buckets[key] = element
	if entry.bucket.tokens < 1 {
		return false
	}
	entry.bucket.tokens--
	return true
}

// admission is the outcome of a single rate-limit decision.
type admission struct {
	allowed bool
	code    string
}

// reserve applies the per-IP and process-wide request budgets plus the
// concurrency budget. On success the caller must call release when the handler
// returns.
func (l *IPRateLimiter) reserve(key string) admission {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	if !l.allowIPLocked(key, now) {
		return admission{code: rateLimitCodePerIP}
	}

	processCapacity := float64(l.config.ProcessRequestsPerMinute)
	processBucket := tokenBucket{tokens: l.processTokens, last: l.processLast}
	refill(&processBucket, now, processCapacity, processCapacity)
	l.processTokens, l.processLast = processBucket.tokens, processBucket.last
	if l.processTokens < 1 {
		return admission{code: rateLimitCodeProcessRequests}
	}

	if l.inFlight >= l.config.ProcessMaxInFlight {
		return admission{code: rateLimitCodeProcessInFlight}
	}

	l.processTokens--
	l.inFlight++
	return admission{allowed: true}
}

// release returns the concurrency token and, when bodyBytes is positive,
// charges the process-wide byte budget.
func (l *IPRateLimiter) release(bodyBytes int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight > 0 {
		l.inFlight--
	}
	if bodyBytes <= 0 {
		return
	}
	now := l.now()
	capacity := float64(l.config.ProcessBytesPerMinute)
	byteBucket := tokenBucket{tokens: l.byteTokens, last: l.byteLast}
	refill(&byteBucket, now, capacity, capacity)
	l.byteTokens, l.byteLast = byteBucket.tokens, byteBucket.last
	l.byteTokens -= float64(bodyBytes)
}

// RateLimit wraps the IPRateLimiter as gin middleware. Rejected requests use the
// standard envelope with apperr.KindRateLimited; the log line carries the route
// and request ID only — never the request body or the raw client address.
func RateLimit(limiter *IPRateLimiter, logger *zap.Logger) gin.HandlerFunc {
	if limiter == nil {
		return func(c *gin.Context) { c.Next() }
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return func(c *gin.Context) {
		// ClientIP reflects the TCP peer because the router disables trusted
		// proxies. Forwarding headers are never consulted.
		key := c.ClientIP()
		result := limiter.reserve(key)
		if !result.allowed {
			logger.Warn("client log request rejected by rate limit",
				zap.String("route", c.FullPath()),
				zap.String("reason", result.code),
				zap.String("clientIp", logsafe.IP(key)),
				zap.String("requestId", GetRequestID(c)),
			)
			httputil.WriteError(c, apperr.RateLimited(result.code, "Rate limit exceeded", nil))
			c.Abort()
			return
		}
		defer limiter.release(c.Request.ContentLength)
		c.Next()
	}
}
