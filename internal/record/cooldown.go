package record

import (
	"sync"
	"time"
)

// DefaultCooldown is the default deduplication / cooldown duration for tracking targets.
const DefaultCooldown = 3 * time.Second

// CooldownManager provides thread-safe target tracking cooldown and deduplication.
type CooldownManager struct {
	mu       sync.Mutex
	cooldown time.Duration
	records  map[string]time.Time
}

// NewCooldownManager creates a CooldownManager with the given cooldown window.
func NewCooldownManager(cooldown time.Duration) *CooldownManager {
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	return &CooldownManager{
		cooldown: cooldown,
		records:  make(map[string]time.Time),
	}
}

// Allow checks if the given key is allowed to trigger a persistent event.
// If the key was seen within the cooldown window, it returns false.
// Otherwise, it records the event time and returns true.
func (cm *CooldownManager) Allow(key string, now time.Time) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Lazy clean if map grows large (> 5000 items)
	if len(cm.records) > 5000 {
		cutoff := now.Add(-2 * cm.cooldown)
		for k, t := range cm.records {
			if t.Before(cutoff) {
				delete(cm.records, k)
			}
		}
	}

	lastTime, exists := cm.records[key]
	if exists && now.Sub(lastTime) < cm.cooldown {
		return false
	}

	cm.records[key] = now
	return true
}

// Reset clears all cooldown records.
func (cm *CooldownManager) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.records = make(map[string]time.Time)
}
