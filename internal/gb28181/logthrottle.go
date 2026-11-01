package gb28181

import (
	"sync"
	"time"
)

// logThrottle collapses repeated diagnostics from the same source into one line per
// window.
//
// It exists because the ingress answers and logs per packet: rejecting a flood one
// datagram at a time is correct, but writing one log line per datagram is how a log
// flood becomes a second outage. The counters stay exact — only the text is
// sampled — so "how bad was it" stays answerable.
//
// The table is bounded: when it is full the window state is dropped wholesale rather
// than grown per key, because the key is derived from attacker-controlled input.
type logThrottle struct {
	mu      sync.Mutex
	window  time.Duration
	maxKeys int
	seen    map[string]throttleEntry
	now     func() time.Time
}

type throttleEntry struct {
	last       time.Time
	suppressed int
}

func newLogThrottle(window time.Duration, maxKeys int) *logThrottle {
	if window <= 0 {
		window = defaultLogThrottleWindow
	}
	if maxKeys <= 0 {
		maxKeys = defaultLogThrottleKeys
	}
	return &logThrottle{
		window:  window,
		maxKeys: maxKeys,
		seen:    make(map[string]throttleEntry),
		now:     time.Now,
	}
}

// allow reports whether an occurrence for key should be logged. The second result is
// how many occurrences were suppressed since the previous allowed line, so the line
// that does get written carries the real volume.
func (t *logThrottle) allow(key string) (log bool, suppressed int) {
	if t == nil {
		return true, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	entry, ok := t.seen[key]
	if ok && now.Sub(entry.last) < t.window {
		entry.suppressed++
		t.seen[key] = entry
		return false, 0
	}
	if !ok && len(t.seen) >= t.maxKeys {
		// Bounded memory beats perfect accounting: after a reset every key logs
		// once more, which is the failure mode we can live with.
		clear(t.seen)
	}
	t.seen[key] = throttleEntry{last: now}
	return true, entry.suppressed
}
