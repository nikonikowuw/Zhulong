package record

import (
	"slices"
	"sync"
	"time"
)

// DefaultCooldown is the default deduplication / cooldown duration for tracking targets.
const DefaultCooldown = 3 * time.Second

// CooldownManager provides thread-safe target tracking cooldown and deduplication.
type CooldownManager struct {
	mu          sync.Mutex
	cooldown    time.Duration
	records     map[string]time.Time
	lastCleaned time.Time
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
	if key == "" {
		return true
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.lazyCleanLocked(now)

	lastTime, exists := cm.records[key]
	if exists {
		diff := now.Sub(lastTime)
		if diff >= 0 && diff < cm.cooldown {
			return false
		}
	}

	cm.records[key] = now
	return true
}

// AllowAll checks if all given keys are allowed to trigger a persistent event.
// If ANY key was seen within the cooldown window, it atomically rejects and
// returns false without updating any timestamps.
// Otherwise, it records now for all keys and returns true.
func (cm *CooldownManager) AllowAll(keys []string, now time.Time) bool {
	if len(keys) == 0 {
		return true
	}
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.lazyCleanLocked(now)

	// 过滤空键并去重。键数极少（识别最多 2 个、车辆 2 个），线性扫描比额外
	// 维护一张 map 更省 —— 这条路径每帧每个目标都会走一次。
	validKeys := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" || slices.Contains(validKeys, k) {
			continue
		}
		validKeys = append(validKeys, k)
	}

	if len(validKeys) == 0 {
		return true
	}

	// Atomic check: any key in cooldown causes rejection
	for _, k := range validKeys {
		if lastTime, exists := cm.records[k]; exists {
			diff := now.Sub(lastTime)
			if diff >= 0 && diff < cm.cooldown {
				return false
			}
		}
	}

	// Atomic update: only when all keys passed
	for _, k := range validKeys {
		cm.records[k] = now
	}
	return true
}

// Window returns the current cooldown duration in a thread-safe manner.
func (cm *CooldownManager) Window() time.Duration {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.cooldown
}

func (cm *CooldownManager) lazyCleanLocked(now time.Time) {
	// 满足两类条件之一即清理：
	// 1. 规模达到 5000 且距离上次清理超 30s（高频激进清理）
	// 2. 距离上次清理超 10 分钟且非空（低频兜底清理，避免冷启动低吞吐时僵尸键滞留）
	isLarge := len(cm.records) > 5000 && (cm.lastCleaned.IsZero() || now.Sub(cm.lastCleaned) >= 30*time.Second)
	isPeriodic := len(cm.records) > 0 && (cm.lastCleaned.IsZero() || now.Sub(cm.lastCleaned) >= 10*time.Minute)

	if !isLarge && !isPeriodic {
		return
	}

	cutoff := now.Add(-2 * cm.cooldown)
	for k, t := range cm.records {
		if t.Before(cutoff) {
			delete(cm.records, k)
		}
	}
	cm.lastCleaned = now
}

// SetCooldown updates the cooldown window in a thread-safe manner.
func (cm *CooldownManager) SetCooldown(d time.Duration) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if d > 0 {
		cm.cooldown = d
	}
}

// Reset clears all cooldown records.
func (cm *CooldownManager) Reset() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.records = make(map[string]time.Time)
	cm.lastCleaned = time.Time{}
}
