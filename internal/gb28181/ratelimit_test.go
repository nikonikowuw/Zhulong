package gb28181

import (
	"fmt"
	"testing"
	"time"
)

func TestRateLimiterAllowsBurstThenRefuses(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	limiter := newRateLimiter(3, time.Second)
	limiter.now = func() time.Time { return now }

	for i := range 3 {
		if !limiter.allow("192.168.1.10") {
			t.Fatalf("request %d must be allowed within the burst", i)
		}
	}
	if limiter.allow("192.168.1.10") {
		t.Fatal("the fourth request must be refused: the burst is exhausted")
	}
	// A different source has its own budget: rate limiting is per peer.
	if !limiter.allow("192.168.1.11") {
		t.Fatal("a different source must not inherit the exhausted bucket")
	}

	now = now.Add(2 * time.Second)
	if !limiter.allow("192.168.1.10") {
		t.Fatal("the bucket must refill over time")
	}
	if limiter.Dropped() == 0 {
		t.Fatal("refusals must be counted for the runtime snapshot")
	}
}

func TestRateLimiterCapsBucketCount(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	limiter := newRateLimiter(1, time.Second)
	limiter.now = func() time.Time { return now }
	limiter.maxKeys = 16

	// Simulate a spoofed-source flood: the table must stay bounded while still
	// answering new peers.
	for i := range 200 {
		source := fmt.Sprintf("10.0.%d.%d", i/256, i%256)
		now = now.Add(time.Millisecond)
		if !limiter.allow(source) {
			t.Fatalf("a fresh source must be allowed: %s", source)
		}
	}
	if len(limiter.buckets) > limiter.maxKeys {
		t.Fatalf("bucket table grew to %d entries, cap is %d", len(limiter.buckets), limiter.maxKeys)
	}
}

func TestRateLimiterRefusesEmptyKey(t *testing.T) {
	limiter := newRateLimiter(1, time.Second)
	if limiter.allow("") {
		t.Fatal("a request without a source address must not be admitted")
	}
}

func TestRateLimiterKeepsFreshBucketsWhenFull(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	// One bucket needs burst*refill = 10s to refill completely.
	limiter := newRateLimiter(1, 10*time.Second)
	limiter.now = func() time.Time { return now }
	limiter.maxKeys = 4

	for _, source := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"} {
		if !limiter.allow(source) {
			t.Fatalf("a fresh source must be allowed: %s", source)
		}
		now = now.Add(time.Millisecond)
	}

	// The table is full but nothing has refilled yet, so eviction must drop the least
	// recently seen third instead of treating every bucket as disposable. A seconds
	// count mixed into a Duration overflows the threshold and wipes the whole table,
	// which would let a spoofed-source flood reset the rate limiter at will.
	if !limiter.allow("10.0.0.5") {
		t.Fatal("a new source must be admitted while the table is bounded")
	}
	if len(limiter.buckets) > limiter.maxKeys {
		t.Fatalf("bucket table grew to %d entries, cap is %d", len(limiter.buckets), limiter.maxKeys)
	}
	if _, ok := limiter.buckets["10.0.0.4"]; !ok {
		t.Fatal("the most recently seen bucket must survive eviction")
	}
}

func TestRateLimiterDropsBucketsThatAlreadyRefilled(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	limiter := newRateLimiter(1, time.Second)
	limiter.now = func() time.Time { return now }
	limiter.maxKeys = 4

	for _, source := range []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "10.0.0.4"} {
		limiter.allow(source)
		now = now.Add(time.Millisecond)
	}

	// Past the refill window every bucket is full again, so it carries no information
	// and must be reclaimed before the least-recently-seen third is sacrificed.
	now = now.Add(time.Minute)
	if !limiter.allow("10.0.0.9") {
		t.Fatal("a new source must be admitted after the table idled")
	}
	if len(limiter.buckets) != 1 {
		t.Fatalf("expected only the new bucket to remain, got %d", len(limiter.buckets))
	}
}
