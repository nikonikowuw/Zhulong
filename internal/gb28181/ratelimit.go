package gb28181

import (
	"slices"
	"sync"
	"time"
)

// Rate limit defaults for the SIP ingress. They bound how much work a single source
// address can create: SIP runs over UDP, where a source address is trivially spoofed,
// so the table is bounded and evicts oldest entries rather than growing per packet.
// Burst and refill are ingress limits (see ingressLimits in sip_limits.go); only the
// table bound is private to the limiter.
const defaultRateMaxSource = 1024

// tokenBucket is a single source's budget.
type tokenBucket struct {
	tokens   float64
	lastSeen time.Time
}

// rateLimiter is a per-key token bucket with bounded memory.
//
// It is deliberately coarse (one bucket per source address, shared by every method):
// the goal is to stop a device from being locked out by a register storm, not to
// enforce per-method fairness.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	burst   float64
	refill  time.Duration
	maxKeys int
	now     func() time.Time
	// dropped counts refusals so the runtime snapshot can show that inbound
	// protection is actually firing in the field.
	dropped uint64
}

func newRateLimiter(burst int, refill time.Duration) *rateLimiter {
	if burst <= 0 {
		burst = defaultSIPRateBurst
	}
	if refill <= 0 {
		refill = defaultSIPRateRefill
	}
	return &rateLimiter{
		buckets: make(map[string]*tokenBucket),
		burst:   float64(burst),
		refill:  refill,
		maxKeys: defaultRateMaxSource,
		now:     time.Now,
	}
}

// allow consumes one token for key. An unknown key starts with a full bucket so a
// device that has been quiet for a while is never penalised for its first request.
func (l *rateLimiter) allow(key string) bool {
	if key == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	bucket, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			l.evictLocked(now)
		}
		bucket = &tokenBucket{tokens: l.burst, lastSeen: now}
		l.buckets[key] = bucket
	}

	elapsed := now.Sub(bucket.lastSeen)
	if elapsed > 0 {
		bucket.tokens = min(l.burst, bucket.tokens+float64(elapsed)/float64(l.refill))
	}
	bucket.lastSeen = now

	if bucket.tokens < 1 {
		l.dropped++
		return false
	}
	bucket.tokens--
	return true
}

// evictLocked drops buckets that have refilled completely (they carry no information
// any more) and, if that is not enough, the least recently seen third of the table.
func (l *rateLimiter) evictLocked(now time.Time) {
	// One bucket goes from empty to full in burst refill intervals. Multiplying the
	// duration (not a "seconds" count) keeps the threshold in the right unit: a
	// seconds count overflows time.Duration and inverts the comparison, which would
	// wipe the whole table on every new peer.
	fullRefill := l.refill * time.Duration(l.burst)
	for key, bucket := range l.buckets {
		if now.Sub(bucket.lastSeen) > fullRefill {
			delete(l.buckets, key)
		}
	}
	if len(l.buckets) < l.maxKeys {
		return
	}
	type entry struct {
		key  string
		seen time.Time
	}
	entries := make([]entry, 0, len(l.buckets))
	for key, bucket := range l.buckets {
		entries = append(entries, entry{key: key, seen: bucket.lastSeen})
	}
	// Partial selection would need a heap for little gain: the table is small and
	// this only runs when it is full.
	slices.SortFunc(entries, func(a, b entry) int { return a.seen.Compare(b.seen) })
	for _, stale := range entries[:len(entries)/3] {
		delete(l.buckets, stale.key)
	}
}

// Dropped returns how many requests were refused by the limiter.
func (l *rateLimiter) Dropped() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.dropped
}
