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

func TestCooldownManager_AllowAll_Atomicity(t *testing.T) {
	cm := record.NewCooldownManager(3 * time.Second)
	base := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)

	// 1. Initial AllowAll on two keys should succeed
	if !cm.AllowAll([]string{"keyA", "keyB"}, base) {
		t.Fatalf("expected initial AllowAll to succeed")
	}

	// 2. Partial collision: keyA is in cooldown (1s later), keyC is brand new.
	// AllowAll MUST return false and MUST NOT record keyC!
	tPartial := base.Add(1 * time.Second)
	if cm.AllowAll([]string{"keyA", "keyC"}, tPartial) {
		t.Fatalf("expected AllowAll to fail when keyA is in cooldown")
	}

	// 3. Verify keyC was NOT recorded by testing it individually at tPartial.
	// Since keyC was never recorded, Allow("keyC", tPartial) should succeed!
	if !cm.Allow("keyC", tPartial) {
		t.Fatalf("expected keyC to not have been consumed by failed AllowAll")
	}

	// 4. Empty or invalid keys should succeed
	if !cm.AllowAll(nil, base) {
		t.Fatalf("expected nil keys to succeed")
	}
	if !cm.AllowAll([]string{"", ""}, base) {
		t.Fatalf("expected empty string keys to succeed")
	}

	// 5. Window method
	if cm.Window() != 3*time.Second {
		t.Fatalf("expected Window() 3s, got %v", cm.Window())
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
				cm.Allow("shared_key", now.Add(time.Duration(j)*time.Millisecond))
				if j%10 == 0 {
					cm.SetCooldown(time.Duration(id+1) * 10 * time.Millisecond)
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestCooldownManager_PeriodicClean(t *testing.T) {
	cm := record.NewCooldownManager(2 * time.Second)
	base := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)

	// Add 3 keys (well below 5000 threshold)
	cm.Allow("key1", base)
	cm.Allow("key2", base)
	cm.Allow("key3", base)

	if count := record.CooldownRecordsCountForTest(cm); count != 3 {
		t.Fatalf("expected 3 records, got %d", count)
	}

	// 5 minutes later, an active key arrives. Not yet 10 minutes, so stale keys remain.
	cm.Allow("key_fresh", base.Add(5*time.Minute))
	if count := record.CooldownRecordsCountForTest(cm); count != 4 {
		t.Fatalf("expected 4 records before periodic threshold, got %d", count)
	}

	// 11 minutes later (elapsed >= 10m from start), new event arrives and triggers periodic lazy clean.
	cm.Allow("key_new", base.Add(11*time.Minute))
	if count := record.CooldownRecordsCountForTest(cm); count != 1 {
		t.Fatalf("expected 1 record after periodic cleanup, got %d", count)
	}
}
