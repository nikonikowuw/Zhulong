package systemtime

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// BuildEpoch represents the base boundary time (2026-01-01). Times before this indicate an unsynced RTC/clock.
	BuildEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// SlewThreshold defines maximum offset for linear adjtime phase correction.
	SlewThreshold = 500 * time.Millisecond

	// PanicThreshold defines maximum offset allowed without consecutive verification.
	PanicThreshold = 10 * time.Minute

	// PanicRequiredChecks is the count of consecutive identical deviations needed before stepping a panic-level offset.
	PanicRequiredChecks = 3

	// ErrPanicReview indicates offset is under consecutive verification.
	ErrPanicReview = errors.New("time offset exceeds panic threshold; entering review")
)

// TimeStateMachine governs safe system time transitions and ensures monotonicity.
type TimeStateMachine struct {
	mu             sync.RWMutex
	driver         ClockDriver
	state          string
	initialSynced  bool
	panicCount     int
	lastPanicDelta time.Duration

	lastSyncTime   *time.Time
	lastSyncServer string
	lastOffset     time.Duration
	lastRTT        time.Duration
	lastError      string
}

// NewTimeStateMachine constructs a state machine bound to a ClockDriver.
func NewTimeStateMachine(driver ClockDriver) *TimeStateMachine {
	sm := &TimeStateMachine{
		driver: driver,
		state:  SyncStateUnsynced,
	}

	// Check if current system time is already ahead of BuildEpoch
	curr, err := driver.GetSystemTime()
	if err == nil && curr.After(BuildEpoch) {
		// Device already has plausible modern time
		sm.initialSynced = true
	}
	return sm
}

// ProcessNTPResult handles an NTP probe result through the two-stage safety state machine.
func (sm *TimeStateMachine) ProcessNTPResult(res *SNTPResult) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sysTime, err := sm.driver.GetSystemTime()
	if err != nil {
		sm.state = SyncStateFailed
		sm.lastError = err.Error()
		return fmt.Errorf("get system time: %w", err)
	}

	absOffset := res.Offset
	if absOffset < 0 {
		absOffset = -absOffset
	}

	// Stage 1: Cold boot / Unsynchronized
	if !sm.initialSynced || sysTime.Before(BuildEpoch) {
		target := sysTime.Add(res.Offset)
		if err := sm.driver.SetRealtime(target); err != nil {
			sm.state = SyncStateFailed
			sm.lastError = err.Error()
			return fmt.Errorf("initial step adjustment failed: %w", err)
		}
		_ = sm.driver.WriteRTC(target)

		sm.initialSynced = true
		sm.state = SyncStateSynchronized
		sm.panicCount = 0
		sm.recordSyncSuccess(res)
		return nil
	}

	// Stage 2: Steady state
	// Case 2.1: Micro-offset (|Offset| < 500ms) -> Slew via adjtimex
	if absOffset < SlewThreshold {
		if err := sm.driver.AdjTime(res.Offset); err != nil {
			sm.state = SyncStateFailed
			sm.lastError = err.Error()
			return fmt.Errorf("slew adjtime failed: %w", err)
		}
		// Periodically refresh RTC to keep it aligned
		_ = sm.driver.WriteRTC(sysTime.Add(res.Offset))

		sm.state = SyncStateSynchronized
		sm.panicCount = 0
		sm.recordSyncSuccess(res)
		return nil
	}

	// Case 2.2: Moderate deviation (500ms <= |Offset| < 10m) -> Step
	if absOffset < PanicThreshold {
		target := sysTime.Add(res.Offset)
		if err := sm.driver.SetRealtime(target); err != nil {
			sm.state = SyncStateFailed
			sm.lastError = err.Error()
			return fmt.Errorf("step adjustment failed: %w", err)
		}
		_ = sm.driver.WriteRTC(target)

		sm.state = SyncStateSynchronized
		sm.panicCount = 0
		sm.recordSyncSuccess(res)
		return nil
	}

	// Case 2.3: Panic threshold exceeded (|Offset| >= 10m) -> Continuous multi-sample review
	if sm.panicCount == 0 || durationDiff(res.Offset, sm.lastPanicDelta) < 10*time.Second {
		sm.panicCount++
		sm.lastPanicDelta = res.Offset

		if sm.panicCount < PanicRequiredChecks {
			sm.state = SyncStatePanicReview
			sm.lastError = fmt.Sprintf("offset %v exceeds panic threshold (10m), verification %d/%d",
				res.Offset.Round(time.Millisecond), sm.panicCount, PanicRequiredChecks)
			return ErrPanicReview
		}

		// Verified 3 times consistently: Apply Step
		target := sysTime.Add(res.Offset)
		if err := sm.driver.SetRealtime(target); err != nil {
			sm.state = SyncStateFailed
			sm.lastError = err.Error()
			return fmt.Errorf("panic step adjustment failed: %w", err)
		}
		_ = sm.driver.WriteRTC(target)

		sm.state = SyncStateSynchronized
		sm.panicCount = 0
		sm.recordSyncSuccess(res)
		return nil
	}

	// Inconsistent panic readings: reset counter and hold
	sm.panicCount = 1
	sm.lastPanicDelta = res.Offset
	sm.state = SyncStatePanicReview
	sm.lastError = fmt.Sprintf("fluctuating large offset %v, verification restarted 1/%d",
		res.Offset.Round(time.Millisecond), PanicRequiredChecks)
	return ErrPanicReview
}

// ProcessManualTime explicitly sets the system time (Manual / Browser sync).
func (sm *TimeStateMachine) ProcessManualTime(target time.Time) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if err := sm.driver.SetRealtime(target); err != nil {
		return fmt.Errorf("set realtime failed: %w", err)
	}
	_ = sm.driver.WriteRTC(target)

	sm.initialSynced = true
	sm.state = SyncStateSynchronized
	sm.panicCount = 0

	now := time.Now().UTC()
	sm.lastSyncTime = &now
	sm.lastSyncServer = "manual"
	sm.lastOffset = 0
	sm.lastRTT = 0
	sm.lastError = ""
	return nil
}

// ProcessSyncError records an NTP probe or network failure.
func (sm *TimeStateMachine) ProcessSyncError(err error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.state = SyncStateFailed
	sm.lastError = err.Error()
}

// SetSyncing marks the state as syncing while a query is in-flight.
func (sm *TimeStateMachine) SetSyncing() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.state != SyncStateSynchronized {
		sm.state = SyncStateSyncing
	}
}

// GetSyncStatus retrieves a snapshot of the synchronization state.
func (sm *TimeStateMachine) GetSyncStatus() SyncStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return SyncStatus{
		State:          sm.state,
		LastSyncTime:   sm.lastSyncTime,
		LastSyncServer: sm.lastSyncServer,
		OffsetMs:       float64(sm.lastOffset.Microseconds()) / 1000.0,
		RttMs:          float64(sm.lastRTT.Microseconds()) / 1000.0,
		ErrorMessage:   sm.lastError,
	}
}

func (sm *TimeStateMachine) recordSyncSuccess(res *SNTPResult) {
	now := time.Now().UTC()
	sm.lastSyncTime = &now
	sm.lastSyncServer = res.Server
	sm.lastOffset = res.Offset
	sm.lastRTT = res.RTT
	sm.lastError = ""
}

func durationDiff(a, b time.Duration) time.Duration {
	d := a - b
	if d < 0 {
		return -d
	}
	return d
}
