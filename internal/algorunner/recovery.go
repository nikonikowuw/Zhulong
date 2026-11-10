package algorunner

import (
	"sync"
	"time"
)

// Default recovery parameters.
const (
	DefaultBaseBackoff         = time.Second
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

// normalize fills in defaults for unset or non-positive fields.
//
// It is the single source of truth for recovery option defaults. NewSupervisor
// stores the normalized value because superviseLoop feeds StableRunningThreshold
// straight into time.NewTicker, which panics (not errors) on a non-positive
// interval, and feeds RollbackGuardWindow into time.NewTimer, where a zero
// window silently disables rollback observation.
//
// It uses a value receiver and returns a new value so that shared instances such
// as DefaultRecoveryOptions() are never mutated in place.
func (o RecoveryOptions) normalize() RecoveryOptions {
	if o.InitialBackoff <= 0 {
		o.InitialBackoff = DefaultBaseBackoff
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = DefaultMaxBackoff
	}
	if o.CrashWindow <= 0 {
		o.CrashWindow = DefaultCrashWindow
	}
	if o.MaxCrashes <= 0 {
		o.MaxCrashes = DefaultMaxCrashesInWindow
	}
	if o.StableRunningThreshold <= 0 {
		o.StableRunningThreshold = DefaultStableResetDuration
	}
	if o.RollbackGuardWindow <= 0 {
		o.RollbackGuardWindow = DefaultRollbackGuardWindow
	}
	return o
}

// dependencyStableReset is the uptime after which a dependency failure starts the
// restart ladder over instead of escalating it.
//
// It is a named accessor rather than a direct MaxBackoff comparison at the call
// site because the two questions — "how slow may a restart get?" and "when does
// the dependency count as freshly lost?" — only share a value today; the watcher
// reads better asking the semantic question, and tuning can diverge later without
// touching it.
func (o RecoveryOptions) dependencyStableReset() time.Duration {
	return o.MaxBackoff
}

// RecoveryStateMachine tracks crash history, exponential backoff, and circuit breakers.
//
// The crash count inside the sliding window is len(crashHistory): no separate
// counter is kept, because a counter and the history it counts can drift apart
// and the backoff ladder would then be computed from a number the circuit
// breaker never saw.
type RecoveryStateMachine struct {
	opts           RecoveryOptions
	crashHistory   []time.Time
	circuitTripped bool
	mu             sync.Mutex
}

// NewRecoveryStateMachine creates a state machine instance.
func NewRecoveryStateMachine(opts RecoveryOptions) *RecoveryStateMachine {
	opts = opts.normalize()

	return &RecoveryStateMachine{
		opts: opts,
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
func (sm *RecoveryStateMachine) RecordCrash(
	now time.Time,
) (
	shouldRestart bool,
	delay time.Duration,
	circuitBroken bool,
) {
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

	// Circuit breaker condition: crash count EXCEEDED the allowed quota within
	// the sliding window (see RecordCrash for the quota semantics).
	if len(sm.crashHistory) > sm.opts.MaxCrashes {
		sm.circuitTripped = true
		return false, 0, true
	}

	// Calculate exponential backoff: base * 2^(crashCount - 1) capped at max
	shift := len(sm.crashHistory) - 1
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

// CheckStableReset clears the crash history once uptime reaches the threshold,
// reporting whether there was any history (or tripped breaker) to clear.
func (sm *RecoveryStateMachine) CheckStableReset(uptime time.Duration) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if uptime < sm.opts.StableRunningThreshold {
		return false
	}
	hadState := len(sm.crashHistory) > 0 || sm.circuitTripped
	sm.crashHistory = []time.Time{}
	sm.circuitTripped = false
	return hadState
}

// Reset clears all crash history and circuit state (e.g., when operator explicitly updates package).
func (sm *RecoveryStateMachine) Reset() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.crashHistory = []time.Time{}
	sm.circuitTripped = false
}

// IsCircuitBroken returns whether the circuit breaker has tripped.
func (sm *RecoveryStateMachine) IsCircuitBroken() bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.circuitTripped
}
