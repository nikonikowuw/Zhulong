package algorithm

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// Default recovery parameters
const (
	DefaultBaseBackoff         = 1 * time.Second
	DefaultMaxBackoff          = 30 * time.Second
	DefaultCrashWindow         = 5 * time.Minute
	DefaultMaxCrashesInWindow  = 3
	DefaultStableResetDuration = 10 * time.Minute
	DefaultRollbackGuardWindow = 5 * time.Second
)

// RecoveryOptions configures the bounded self-healing parameters.
type RecoveryOptions struct {
	InitialBackoff         time.Duration
	MaxBackoff             time.Duration
	CrashWindow            time.Duration
	MaxCrashes             int
	StableRunningThreshold time.Duration
	RollbackGuardWindow    time.Duration
}

// DefaultRecoveryOptions returns production-ready recovery parameters.
func DefaultRecoveryOptions() RecoveryOptions {
	return RecoveryOptions{
		InitialBackoff:         DefaultBaseBackoff,
		MaxBackoff:             DefaultMaxBackoff,
		CrashWindow:            DefaultCrashWindow,
		MaxCrashes:             DefaultMaxCrashesInWindow,
		StableRunningThreshold: DefaultStableResetDuration,
		RollbackGuardWindow:    DefaultRollbackGuardWindow,
	}
}

// RecoveryStateMachine tracks crash history, exponential backoff, and circuit breakers.
type RecoveryStateMachine struct {
	opts           RecoveryOptions
	consecutive    int
	crashHistory   []time.Time
	lastStableAt   time.Time
	circuitTripped bool
	mu             sync.Mutex
}

// NewRecoveryStateMachine creates a state machine instance.
func NewRecoveryStateMachine(opts RecoveryOptions) *RecoveryStateMachine {
	if opts.InitialBackoff <= 0 {
		opts.InitialBackoff = DefaultBaseBackoff
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = DefaultMaxBackoff
	}
	if opts.CrashWindow <= 0 {
		opts.CrashWindow = DefaultCrashWindow
	}
	if opts.MaxCrashes <= 0 {
		opts.MaxCrashes = DefaultMaxCrashesInWindow
	}
	if opts.StableRunningThreshold <= 0 {
		opts.StableRunningThreshold = DefaultStableResetDuration
	}
	if opts.RollbackGuardWindow <= 0 {
		opts.RollbackGuardWindow = DefaultRollbackGuardWindow
	}

	return &RecoveryStateMachine{
		opts:         opts,
		lastStableAt: time.Now(),
	}
}

// RecordCrash registers a process crash and calculates the next backoff delay.
//
// MaxCrashes is the number of crashes ALLOWED within CrashWindow (an explicit
// quota, not an exclusive threshold). The circuit breaker trips only when the
// crash count EXCEEDS that quota, i.e. crash #(MaxCrashes+1) trips it. With the
// default MaxCrashes=3, crashes 1..3 restart and crash 4 breaks the circuit.
//
// Backoff ladder: base * 2^(n-1) capped at MaxBackoff. With a 3-crash quota the
// observable delays are 1s, 2s, 4s; the 8s tier (shift=3) is UNREACHABLE under
// this quota because crash 4 trips the breaker instead of scheduling 8s. The
// ladder describes the formula's shape, not a guarantee that every tier runs.
func (sm *RecoveryStateMachine) RecordCrash(now time.Time) (shouldRestart bool, delay time.Duration, circuitBroken bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.circuitTripped {
		return false, 0, true
	}

	// Prune old crashes outside the sliding window
	cutoff := now.Add(-sm.opts.CrashWindow)
	validHistory := make([]time.Time, 0, len(sm.crashHistory)+1)
	for _, t := range sm.crashHistory {
		if t.After(cutoff) {
			validHistory = append(validHistory, t)
		}
	}
	validHistory = append(validHistory, now)
	sm.crashHistory = validHistory
	sm.consecutive = len(validHistory)

	// Circuit breaker condition: crash count EXCEEDED the allowed quota within
	// the sliding window (see RecordCrash for the quota semantics).
	if len(sm.crashHistory) > sm.opts.MaxCrashes {
		sm.circuitTripped = true
		return false, 0, true
	}

	// Calculate exponential backoff: base * 2^(consecutive - 1) capped at max
	shift := sm.consecutive - 1
	if shift > 10 {
		shift = 10
	}
	factor := time.Duration(1 << shift)
	delay = sm.opts.InitialBackoff * factor
	if delay > sm.opts.MaxBackoff {
		delay = sm.opts.MaxBackoff
	}

	return true, delay, false
}

// CheckStableReset checks if stable uptime exceeds the threshold to reset crash counts.
func (sm *RecoveryStateMachine) CheckStableReset(uptime time.Duration) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if uptime >= sm.opts.StableRunningThreshold {
		sm.consecutive = 0
		sm.crashHistory = nil
		sm.circuitTripped = false
		sm.lastStableAt = time.Now()
		return true
	}
	return false
}

// Reset clears all crash history and circuit state (e.g., when operator explicitly updates package).
func (sm *RecoveryStateMachine) Reset() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.consecutive = 0
	sm.crashHistory = nil
	sm.circuitTripped = false
	sm.lastStableAt = time.Now()
}

// IsCircuitBroken returns whether the circuit breaker has tripped.
func (sm *RecoveryStateMachine) IsCircuitBroken() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.circuitTripped
}

// RollbackGuard manages observation period for newly upgraded packages.
type RollbackGuard struct {
	pkgID           string
	newVersion      string
	previousVersion string
	startedAt       time.Time
	guardWindow     time.Duration
	observedDone    bool
	mu              sync.Mutex
	logger          *zap.Logger
}

// NewRollbackGuard creates a guard to observe newly upgraded package stability.
func NewRollbackGuard(pkgID, newVersion, prevVersion string, guardWindow time.Duration, logger *zap.Logger) *RollbackGuard {
	if logger == nil {
		logger = zap.NewNop()
	}
	if guardWindow <= 0 {
		guardWindow = DefaultRollbackGuardWindow
	}
	return &RollbackGuard{
		pkgID:           pkgID,
		newVersion:      newVersion,
		previousVersion: prevVersion,
		startedAt:       time.Now(),
		guardWindow:     guardWindow,
		logger:          logger.With(zap.String("package", pkgID)),
	}
}

// ShouldRollback returns true if a crash occurred during the initial observation window
// and a valid previous version exists.
func (g *RollbackGuard) ShouldRollback(crashTime time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.observedDone || g.previousVersion == "" {
		return false
	}

	if crashTime.Sub(g.startedAt) <= g.guardWindow {
		g.logger.Warn("new package version crashed within observation window, triggering rollback",
			zap.String("new_version", g.newVersion),
			zap.String("rollback_to_version", g.previousVersion),
			zap.Duration("elapsed", crashTime.Sub(g.startedAt)),
		)
		g.observedDone = true
		return true
	}

	g.observedDone = true
	return false
}

// MarkStable marks the observation window as successfully completed without rollback.
func (g *RollbackGuard) MarkStable() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.observedDone = true
}

// PreviousVersion returns the fallback version.
func (g *RollbackGuard) PreviousVersion() string {
	return g.previousVersion
}
