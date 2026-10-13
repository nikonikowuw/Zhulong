package algorithm_test

import (
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"go.uber.org/zap"
)

func TestRecoveryStateMachineExponentialBackoffAndCircuitBreaker(t *testing.T) {
	opts := algorithm.RecoveryOptions{
		MaxCrashes:             3,
		CrashWindow:            5 * time.Minute,
		InitialBackoff:         1 * time.Second,
		MaxBackoff:             30 * time.Second,
		StableRunningThreshold: 10 * time.Minute,
	}
	sm := algorithm.NewRecoveryStateMachine(opts)
	baseTime := time.Now()

	// 1st crash: backoff should be 1s
	restart, delay, broken := sm.RecordCrash(baseTime)
	if !restart || broken {
		t.Errorf("1st crash should restart and not be broken")
	}
	if delay != 1*time.Second {
		t.Errorf("expected 1s delay, got: %v", delay)
	}

	// 2nd crash (1s later): backoff should be 2s
	restart, delay, broken = sm.RecordCrash(baseTime.Add(1 * time.Second))
	if !restart || broken {
		t.Errorf("2nd crash should restart and not be broken")
	}
	if delay != 2*time.Second {
		t.Errorf("expected 2s delay, got: %v", delay)
	}

	// 3rd crash (4s later): still within the allowed quota of 3 -> restart, 4s backoff.
	restart, delay, broken = sm.RecordCrash(baseTime.Add(3 * time.Second))
	if !restart || broken {
		t.Errorf("3rd crash should restart and not be broken (MaxCrashes=3 is an allowed quota)")
	}
	if delay != 4*time.Second {
		t.Errorf("expected 4s delay on 3rd crash, got: %v", delay)
	}

	// 4th crash: exceeds the quota -> circuit breaks, no restart.
	restart, delay, broken = sm.RecordCrash(baseTime.Add(4 * time.Second))
	if restart || !broken {
		t.Errorf("4th crash should trigger circuit breaker (no restart)")
	}
	if !sm.IsCircuitBroken() {
		t.Errorf("expected IsCircuitBroken() to be true")
	}

	// Reset
	sm.Reset()
	if sm.IsCircuitBroken() {
		t.Errorf("expected IsCircuitBroken() to be false after Reset()")
	}
}

func TestRecoveryStateMachinePrunesOldCrashes(t *testing.T) {
	opts := algorithm.RecoveryOptions{
		MaxCrashes:             3,
		CrashWindow:            5 * time.Minute,
		InitialBackoff:         1 * time.Second,
		MaxBackoff:             30 * time.Second,
		StableRunningThreshold: 10 * time.Minute,
	}
	sm := algorithm.NewRecoveryStateMachine(opts)
	baseTime := time.Now()

	// 1st crash 10 minutes ago (outside 5 min window)
	sm.RecordCrash(baseTime.Add(-10 * time.Minute))

	// 2nd crash 6 minutes ago (outside 5 min window)
	sm.RecordCrash(baseTime.Add(-6 * time.Minute))

	// 3rd crash now
	restart, delay, broken := sm.RecordCrash(baseTime)
	// Old crashes should have been pruned, so this is effectively the 1st crash in window!
	if !restart || broken {
		t.Errorf("expected restart=true, broken=false since old crashes were pruned")
	}
	if delay != 1*time.Second {
		t.Errorf("expected 1s delay, got: %v", delay)
	}
}

func TestRecoveryStateMachineStableRunningReset(t *testing.T) {
	opts := algorithm.RecoveryOptions{
		MaxCrashes:             3,
		CrashWindow:            5 * time.Minute,
		InitialBackoff:         1 * time.Second,
		MaxBackoff:             30 * time.Second,
		StableRunningThreshold: 10 * time.Minute,
	}
	sm := algorithm.NewRecoveryStateMachine(opts)

	sm.RecordCrash(time.Now())

	// Running for 5 minutes (less than 10 min threshold)
	if sm.CheckStableReset(5 * time.Minute) {
		t.Errorf("should not reset before 10 minutes threshold")
	}

	// Running for 11 minutes (exceeds 10 min threshold)
	if !sm.CheckStableReset(11 * time.Minute) {
		t.Errorf("should reset after 10 minutes stable running")
	}

	// Subsequent crash should be treated as 1st crash again
	restart, delay, broken := sm.RecordCrash(time.Now())
	if !restart || broken || delay != 1*time.Second {
		t.Errorf("expected clean 1s backoff after reset, got delay: %v", delay)
	}
}

func TestRollbackGuardWindow(t *testing.T) {
	guardWindow := 2 * time.Second
	guard := algorithm.NewRollbackGuard("pkg_01", "2.0.0", "1.0.0", guardWindow, zap.NewNop())

	// Crash at 500ms (within 2s window) -> Should Rollback!
	crashTime := time.Now().Add(500 * time.Millisecond)
	if !guard.ShouldRollback(crashTime) {
		t.Errorf("expected ShouldRollback=true when crashing within guard window")
	}

	// Second check should be false since already handled
	if guard.ShouldRollback(crashTime) {
		t.Errorf("expected ShouldRollback=false after already handled")
	}

	// New guard: crash outside guard window -> Should NOT rollback!
	guard2 := algorithm.NewRollbackGuard("pkg_02", "2.0.0", "1.0.0", 100*time.Millisecond, zap.NewNop())
	time.Sleep(150 * time.Millisecond)
	if guard2.ShouldRollback(time.Now()) {
		t.Errorf("expected ShouldRollback=false when crashing after guard window elapsed")
	}

	// Guard with no previous version -> Should NOT rollback
	guard3 := algorithm.NewRollbackGuard("pkg_03", "1.0.0", "", guardWindow, zap.NewNop())
	if guard3.ShouldRollback(time.Now()) {
		t.Errorf("expected ShouldRollback=false when no previous version exists")
	}
}
