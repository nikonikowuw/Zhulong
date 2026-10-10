package systemtime

import (
	"errors"
	"testing"
	"time"
)

func TestStateMachineColdBootStep(t *testing.T) {
	driver := NewStubClockDriver()
	// Set system time to year 2020 (before BuildEpoch 2026-01-01)
	pastTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	driver.offset = time.Until(pastTime)

	sm := NewTimeStateMachine(driver)

	// NTP returns modern time with offset ~ 6 years
	modernTime := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	res := &SNTPResult{
		Server: "ntp.test.com",
		Offset: modernTime.Sub(pastTime),
		RTT:    20 * time.Millisecond,
	}

	err := sm.ProcessNTPResult(res)
	if err != nil {
		t.Fatalf("expected cold boot step to succeed, got %v", err)
	}

	stepCalls := driver.GetStepCalls()
	if len(stepCalls) != 1 {
		t.Fatalf("expected 1 step call, got %d", len(stepCalls))
	}

	rtcWrites := driver.GetRTCWrites()
	if len(rtcWrites) != 1 {
		t.Fatalf("expected 1 rtc write call, got %d", len(rtcWrites))
	}

	status := sm.GetSyncStatus()
	if status.State != SyncStateSynchronized {
		t.Errorf("expected state synchronized, got %s", status.State)
	}
}

func TestStateMachineSteadyStateSlew(t *testing.T) {
	driver := NewStubClockDriver()
	// Current time is in 2026
	driver.offset = 0

	sm := NewTimeStateMachine(driver)

	// Small offset: 250ms (< 500ms)
	res := &SNTPResult{
		Server: "ntp.test.com",
		Offset: 250 * time.Millisecond,
		RTT:    15 * time.Millisecond,
	}

	err := sm.ProcessNTPResult(res)
	if err != nil {
		t.Fatalf("expected slew to succeed, got %v", err)
	}

	slewCalls := driver.GetSlewCalls()
	if len(slewCalls) != 1 || slewCalls[0] != 250*time.Millisecond {
		t.Fatalf("expected 1 slew call with 250ms, got %v", slewCalls)
	}

	stepCalls := driver.GetStepCalls()
	if len(stepCalls) != 0 {
		t.Fatalf("expected 0 step calls during slew, got %d", len(stepCalls))
	}
}

func TestStateMachineSteadyStateModerateStep(t *testing.T) {
	driver := NewStubClockDriver()
	driver.offset = 0

	sm := NewTimeStateMachine(driver)

	// Moderate offset: 5 seconds (500ms <= offset < 10m)
	res := &SNTPResult{
		Server: "ntp.test.com",
		Offset: 5 * time.Second,
		RTT:    25 * time.Millisecond,
	}

	err := sm.ProcessNTPResult(res)
	if err != nil {
		t.Fatalf("expected moderate step to succeed, got %v", err)
	}

	stepCalls := driver.GetStepCalls()
	if len(stepCalls) != 1 {
		t.Fatalf("expected 1 step call, got %d", len(stepCalls))
	}
}

func TestStateMachineSteadyStatePanicReview(t *testing.T) {
	driver := NewStubClockDriver()
	driver.offset = 0

	sm := NewTimeStateMachine(driver)

	// Huge offset: 15 minutes (>= 10m)
	res := &SNTPResult{
		Server: "ntp.test.com",
		Offset: 15 * time.Minute,
		RTT:    30 * time.Millisecond,
	}

	// 1st attempt -> ErrPanicReview
	err := sm.ProcessNTPResult(res)
	if !errors.Is(err, ErrPanicReview) {
		t.Fatalf("expected ErrPanicReview on 1st probe, got %v", err)
	}
	if sm.GetSyncStatus().State != SyncStatePanicReview {
		t.Errorf("expected state panic_review, got %s", sm.GetSyncStatus().State)
	}

	// 2nd attempt -> ErrPanicReview
	err = sm.ProcessNTPResult(res)
	if !errors.Is(err, ErrPanicReview) {
		t.Fatalf("expected ErrPanicReview on 2nd probe, got %v", err)
	}

	// 3rd attempt -> Passes and applies Step
	err = sm.ProcessNTPResult(res)
	if err != nil {
		t.Fatalf("expected 3rd probe to confirm panic offset and step, got %v", err)
	}
	if sm.GetSyncStatus().State != SyncStateSynchronized {
		t.Errorf("expected state synchronized after 3 confirmed probes, got %s", sm.GetSyncStatus().State)
	}
	if len(driver.GetStepCalls()) != 1 {
		t.Fatalf("expected 1 step call after 3 panic reviews, got %d", len(driver.GetStepCalls()))
	}
}

func TestStateMachineManualTime(t *testing.T) {
	driver := NewStubClockDriver()
	sm := NewTimeStateMachine(driver)

	manualTarget := time.Date(2026, 12, 25, 8, 0, 0, 0, time.UTC)
	err := sm.ProcessManualTime(manualTarget)
	if err != nil {
		t.Fatalf("ProcessManualTime failed: %v", err)
	}

	if sm.GetSyncStatus().State != SyncStateSynchronized {
		t.Errorf("expected synchronized after manual set, got %s", sm.GetSyncStatus().State)
	}
	if len(driver.GetStepCalls()) != 1 {
		t.Errorf("expected 1 step call for manual set, got %d", len(driver.GetStepCalls()))
	}
	if len(driver.GetRTCWrites()) != 1 {
		t.Errorf("expected 1 rtc write for manual set, got %d", len(driver.GetRTCWrites()))
	}
}
