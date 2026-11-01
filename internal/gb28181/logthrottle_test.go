package gb28181

import (
	"testing"
	"time"
)

func TestLogThrottleCollapsesABurst(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	throttle := newLogThrottle(5*time.Second, 8)
	throttle.now = func() time.Time { return now }

	if log, suppressed := throttle.allow("burst|192.168.1.64"); !log || suppressed != 0 {
		t.Fatalf("the first occurrence must be logged: log=%v suppressed=%d", log, suppressed)
	}
	for i := 1; i <= 10; i++ {
		if log, _ := throttle.allow("burst|192.168.1.64"); log {
			t.Fatalf("occurrence %d should have been suppressed inside the window", i)
		}
	}
	now = now.Add(5 * time.Second)
	log, suppressed := throttle.allow("burst|192.168.1.64")
	if !log {
		t.Fatal("the window must expire")
	}
	if suppressed != 10 {
		t.Fatalf("the line that is written must carry the suppressed volume, got %d", suppressed)
	}
}

func TestLogThrottleIsPerKey(t *testing.T) {
	throttle := newLogThrottle(time.Minute, 8)
	if log, _ := throttle.allow("a"); !log {
		t.Fatal("key a must be logged first")
	}
	if log, _ := throttle.allow("b"); !log {
		t.Fatal("a different key must not be suppressed by another key's window")
	}
}

func TestLogThrottleBoundsItsKeyTable(t *testing.T) {
	throttle := newLogThrottle(time.Minute, 2)
	for _, key := range []string{"a", "b", "c", "d"} {
		if log, _ := throttle.allow(key); !log {
			t.Fatalf("key %q must be logged", key)
		}
	}
	if len(throttle.seen) > 2 {
		t.Fatalf("the key table must stay bounded, got %d entries", len(throttle.seen))
	}
}

func TestLogThrottleNilIsAPassThrough(t *testing.T) {
	var throttle *logThrottle
	if log, suppressed := throttle.allow("any"); !log || suppressed != 0 {
		t.Fatal("a nil throttle must log everything")
	}
}
