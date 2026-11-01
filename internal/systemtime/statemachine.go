package systemtime

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// buildEpoch is the boundary below which the system clock is treated as unsynced
// (2026-01-01). It is a var rather than a const because time.Date is not a
// constant expression, and it stays unexported because it gates whether the
// clock may be stepped: no other package needs to read or reassign it.
var buildEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const (
	// SlewThreshold is the largest offset corrected by adjtime slew instead of a
	// step. Below it the wall clock never jumps.
	SlewThreshold = 500 * time.Millisecond

	// PanicThreshold is the largest offset stepped without consecutive checks.
	PanicThreshold = 10 * time.Minute

	// PanicRequiredChecks is the number of consecutive consistent offsets needed
	// before a panic-level offset may be stepped.
	PanicRequiredChecks = 3

	// panicConsistencyWindow bounds how far two panic-level offsets may drift
	// apart and still count as evidence of the same clock jump.
	panicConsistencyWindow = 10 * time.Second
)

// ErrPanicReview indicates offset is under consecutive verification.
var ErrPanicReview = errors.New("time offset exceeds panic threshold; entering review")

// ActionKind classifies what a single NTP probe actually did to the clock.
//
// The caller uses it to decide whether the probe deserves an audit record. This
// distinction is the whole point: a steady-state slew only nudges the clock by a
// few milliseconds via adjtimex and repeats every sync interval, so recording it
// turns the bounded audit table into a heartbeat log that eventually prunes the
// operations an operator actually needs to trace.
type ActionKind int

const (
	// ActionNone means the probe produced no clock change worth auditing: a
	// steady-state slew below SlewThreshold. Telemetry, not an event.
	ActionNone ActionKind = iota
	// ActionStepped means SetRealtime moved the wall clock. This is a real,
	// user-visible discontinuity in time and always worth auditing.
	ActionStepped
	// ActionPanicReview means the measured offset crossed PanicThreshold and the
	// state machine is holding a multi-sample verification. This is a high-risk
	// condition and must never be reported as a success.
	ActionPanicReview
)

// String renders the kind for logging.
func (k ActionKind) String() string {
	switch k {
	case ActionStepped:
		return "stepped"
	case ActionPanicReview:
		return "panic_review"
	default:
		return "none"
	}
}

// SyncOutcome reports what one probe did, so the service layer can audit only
// the transitions and the real clock changes.
type SyncOutcome struct {
	Kind ActionKind
	// Step is the applied correction. Only meaningful for ActionStepped.
	Step time.Duration
	// Review is the consecutive-verification progress (n of PanicRequiredChecks).
	// Only meaningful for ActionPanicReview.
	Review int
	// RTCErr reports a failed hardware RTC write-back. It never fails the probe:
	// CLOCK_REALTIME already moved, but a stale RTC means the next cold boot
	// restarts from the wrong time, so the caller has to surface it.
	RTCErr error
}

// clockController is the subset of ClockDriver the state machine needs: read the
// kernel clock and apply corrections. RTC health probing and timezone handling
// stay with the driver and the service.
type clockController interface {
	GetSystemTime() (time.Time, error)
	SetRealtime(t time.Time) error
	AdjTime(offset time.Duration) error
	WriteRTC(t time.Time) error
}

// TimeStateMachine governs safe system time transitions and ensures monotonicity.
//
// All exported methods take the lock themselves; the Locked-suffixed helpers
// assume the caller already holds it.
type TimeStateMachine struct {
	mu             sync.RWMutex
	driver         clockController
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

// NewTimeStateMachine constructs a state machine bound to a clockController.
//
// It deliberately performs no driver I/O: the "is this clock already plausible?"
// decision needs a syscall, and constructors in this codebase stay pure. The
// first ProcessNTPResult call makes that decision instead.
func NewTimeStateMachine(driver clockController) *TimeStateMachine {
	return &TimeStateMachine{
		driver: driver,
		state:  SyncStateUnsynced,
	}
}

// ProcessNTPResult handles an NTP probe result through the two-stage safety state machine.
//
// The returned SyncOutcome describes the side effect even when the error is
// non-nil: an ErrPanicReview result still carries ActionPanicReview so the caller
// can audit the high-risk condition as a failure instead of silently dropping it.
func (sm *TimeStateMachine) ProcessNTPResult(res *SNTPResult) (SyncOutcome, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sysTime, err := sm.driver.GetSystemTime()
	if err != nil {
		return SyncOutcome{}, sm.failLocked(fmt.Errorf("get system time: %w", err))
	}

	// A device that boots with plausible time must not be stepped just because
	// this process has never synced: adopt the running clock as the baseline.
	if !sm.initialSynced && sysTime.After(buildEpoch) {
		sm.initialSynced = true
	}

	absOffset := absDuration(res.Offset)

	switch {
	// Stage 1: cold boot / unsynchronized clock.
	case !sm.initialSynced || sysTime.Before(buildEpoch):
		return sm.stepLocked(sysTime.Add(res.Offset), res)

	// Stage 2.1: micro-offset below SlewThreshold -> linear adjtime slew.
	case absOffset < SlewThreshold:
		return sm.slewLocked(sysTime, res)

	// Stage 2.2: moderate deviation below PanicThreshold -> immediate step.
	case absOffset < PanicThreshold:
		return sm.stepLocked(sysTime.Add(res.Offset), res)

	// Stage 2.3: panic-level offset -> consecutive multi-sample review.
	default:
		return sm.reviewPanicLocked(sysTime, res)
	}
}

// ProcessManualTime explicitly sets the system time (manual / browser sync).
func (sm *TimeStateMachine) ProcessManualTime(target time.Time) (SyncOutcome, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if err := sm.driver.SetRealtime(target); err != nil {
		return SyncOutcome{}, sm.failLocked(fmt.Errorf("set realtime to %s: %w",
			target.UTC().Format(time.RFC3339), err))
	}

	rtcErr := writeHardwareRTC(sm.driver, target)

	sm.initialSynced = true
	sm.state = SyncStateSynchronized
	sm.panicCount = 0

	now := time.Now().UTC()
	sm.lastSyncTime = &now
	sm.lastSyncServer = "manual"
	sm.lastOffset = 0
	sm.lastRTT = 0
	sm.lastError = ""
	return SyncOutcome{Kind: ActionStepped, RTCErr: rtcErr}, nil
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
		OffsetMS:       float64(sm.lastOffset.Microseconds()) / 1000.0,
		RTTMS:          float64(sm.lastRTT.Microseconds()) / 1000.0,
		ErrorMessage:   sm.lastError,
	}
}

// stepLocked jumps CLOCK_REALTIME to target and records the success.
func (sm *TimeStateMachine) stepLocked(target time.Time, res *SNTPResult) (SyncOutcome, error) {
	if err := sm.driver.SetRealtime(target); err != nil {
		return SyncOutcome{}, sm.failLocked(fmt.Errorf("step clock to %s: %w",
			target.UTC().Format(time.RFC3339), err))
	}
	rtcErr := writeHardwareRTC(sm.driver, target)

	sm.initialSynced = true
	sm.state = SyncStateSynchronized
	sm.panicCount = 0
	sm.recordSyncSuccessLocked(res)

	return SyncOutcome{Kind: ActionStepped, Step: res.Offset, RTCErr: rtcErr}, nil
}

// slewLocked applies a sub-threshold correction without moving the wall clock.
func (sm *TimeStateMachine) slewLocked(sysTime time.Time, res *SNTPResult) (SyncOutcome, error) {
	if err := sm.driver.AdjTime(res.Offset); err != nil {
		return SyncOutcome{}, sm.failLocked(fmt.Errorf("slew clock by %v: %w", res.Offset, err))
	}
	rtcErr := writeHardwareRTC(sm.driver, sysTime.Add(res.Offset))

	sm.state = SyncStateSynchronized
	sm.panicCount = 0
	sm.recordSyncSuccessLocked(res)

	// A slew only warps the clock frequency; the wall clock never jumps. This
	// repeats every sync interval, so it stays out of the audit trail.
	return SyncOutcome{Kind: ActionNone, RTCErr: rtcErr}, nil
}

// reviewPanicLocked holds a panic-level offset until consecutive probes agree,
// then applies it. Two differing large offsets restart the verification: they
// are not evidence of a single jump, so stepping either one is unsafe.
func (sm *TimeStateMachine) reviewPanicLocked(sysTime time.Time, res *SNTPResult) (SyncOutcome, error) {
	restarted := sm.panicCount > 0 && absDuration(res.Offset-sm.lastPanicDelta) >= panicConsistencyWindow
	if restarted {
		sm.panicCount = 0
	}
	sm.panicCount++
	sm.lastPanicDelta = res.Offset

	if sm.panicCount < PanicRequiredChecks {
		sm.state = SyncStatePanicReview
		if restarted {
			sm.lastError = fmt.Sprintf("fluctuating large offset %v, verification restarted %d/%d",
				res.Offset.Round(time.Millisecond), sm.panicCount, PanicRequiredChecks)
		} else {
			sm.lastError = fmt.Sprintf("offset %v exceeds panic threshold (%v), verification %d/%d",
				res.Offset.Round(time.Millisecond), PanicThreshold, sm.panicCount, PanicRequiredChecks)
		}
		// Carry the outcome alongside the error: the caller must audit this as a
		// failure. Returning only the error previously let the service fall
		// through to a "success" record, which is the exact opposite of the truth
		// for a 15-minute clock jump under verification.
		return SyncOutcome{Kind: ActionPanicReview, Review: sm.panicCount}, ErrPanicReview
	}

	return sm.stepLocked(sysTime.Add(res.Offset), res)
}

// failLocked records a driver-level failure and returns err unchanged.
func (sm *TimeStateMachine) failLocked(err error) error {
	sm.state = SyncStateFailed
	sm.lastError = err.Error()
	return err
}

func (sm *TimeStateMachine) recordSyncSuccessLocked(res *SNTPResult) {
	now := time.Now().UTC()
	sm.lastSyncTime = &now
	sm.lastSyncServer = res.Server
	sm.lastOffset = res.Offset
	sm.lastRTT = res.RTT
	sm.lastError = ""
}

// writeHardwareRTC mirrors the wall clock into the RTC chip. The error is
// returned rather than dropped: the clock already moved, so it cannot fail the
// operation, but a silent failure here is what makes the next cold boot wrong.
func writeHardwareRTC(driver clockController, t time.Time) error {
	if err := driver.WriteRTC(t); err != nil {
		return fmt.Errorf("write hardware rtc: %w", err)
	}
	return nil
}

func absDuration(d time.Duration) time.Duration {
	return max(d, -d)
}
