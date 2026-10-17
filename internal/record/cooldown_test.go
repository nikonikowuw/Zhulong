package record_test

import (
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

func TestCooldownManager_AllowAndExpiration(t *testing.T) {
	cm := record.NewCooldownManager(2 * time.Second)
	base := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)

	// First event allowed
	if !cm.Allow("key1", base) {
		t.Fatalf("expected first event to be allowed")
	}

	// Repeated event within 2s suppressed
	if cm.Allow("key1", base.Add(1*time.Second)) {
		t.Fatalf("expected event within 1s to be suppressed")
	}

	// Event at exact 2s allowed
	if !cm.Allow("key1", base.Add(2*time.Second)) {
		t.Fatalf("expected event at 2s to be allowed")
	}

	// Different key allowed immediately
	if !cm.Allow("key2", base.Add(1*time.Second)) {
		t.Fatalf("expected different key to be allowed")
	}
}

func TestCooldownManager_NegativeTimeDiffNotSuppressed(t *testing.T) {
	cm := record.NewCooldownManager(3 * time.Second)
	base := time.Date(2026, 10, 17, 10, 0, 10, 0, time.UTC)

	// Normal event recorded at 10:00:10
	if !cm.Allow("key1", base) {
		t.Fatalf("expected first event to be allowed")
	}

	// Earlier frame arrives (e.g. 10:00:08, network jitter or stream reset)
	// In the old implementation, diff was -2s, which is < 3s, causing wrongful suppression!
	earlier := base.Add(-2 * time.Second)
	if !cm.Allow("key1", earlier) {
		t.Fatalf("expected earlier/out-of-order frame to NOT be wrongly suppressed")
	}
}

func TestCooldownManager_SetCooldownAndReset(t *testing.T) {
	cm := record.NewCooldownManager(5 * time.Second)
	base := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)

	if !cm.Allow("k", base) {
		t.Fatalf("initial allow failed")
	}

	// Change to 1 second
	cm.SetCooldown(1 * time.Second)

	// 1.5s later should now be allowed under the new 1s cooldown
	if !cm.Allow("k", base.Add(1500*time.Millisecond)) {
		t.Fatalf("expected allowed under updated 1s cooldown")
	}

	// Reset
	cm.Reset()
	if !cm.Allow("k", base.Add(1600*time.Millisecond)) {
		t.Fatalf("expected allowed after reset")
	}
}

func TestCooldownManager_ConcurrentAccess(t *testing.T) {
	cm := record.NewCooldownManager(100 * time.Millisecond)
	var wg sync.WaitGroup
	now := time.Now().UTC()

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = cm.Allow("shared_key", now.Add(time.Duration(j)*time.Millisecond))
				if j%10 == 0 {
					cm.SetCooldown(time.Duration(id+1) * 10 * time.Millisecond)
				}
			}
		}(i)
	}

	wg.Wait()
}
