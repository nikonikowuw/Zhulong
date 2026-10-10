package systemtime

import (
	"testing"
	"time"
)

func TestStubClockDriverBasic(t *testing.T) {
	driver := NewStubClockDriver()

	// 1. Initial status
	if !driver.HasClockPermission() {
		t.Error("expected default permission true")
	}
	if driver.GetRTCStatus() != RtcStatusNormal {
		t.Errorf("expected RTC normal, got %s", driver.GetRTCStatus())
	}

	tz, err := driver.GetTimezone()
	if err != nil || tz != "Asia/Shanghai" {
		t.Errorf("expected Asia/Shanghai, got %s (err: %v)", tz, err)
	}

	// 2. SetRealtime
	target := time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)
	if err := driver.SetRealtime(target); err != nil {
		t.Fatalf("SetRealtime failed: %v", err)
	}
	calls := driver.GetStepCalls()
	if len(calls) != 1 || !calls[0].Equal(target) {
		t.Fatalf("unexpected step calls: %v", calls)
	}

	// 3. AdjTime
	offset := 200 * time.Millisecond
	if err := driver.AdjTime(offset); err != nil {
		t.Fatalf("AdjTime failed: %v", err)
	}
	slewCalls := driver.GetSlewCalls()
	if len(slewCalls) != 1 || slewCalls[0] != offset {
		t.Fatalf("unexpected slew calls: %v", slewCalls)
	}

	// 4. RTC Read/Write
	rtcTarget := time.Date(2026, 11, 1, 8, 30, 0, 0, time.UTC)
	if err := driver.WriteRTC(rtcTarget); err != nil {
		t.Fatalf("WriteRTC failed: %v", err)
	}
	readRtc, err := driver.ReadRTC()
	if err != nil || !readRtc.Equal(rtcTarget) {
		t.Fatalf("expected RTC time %v, got %v (err: %v)", rtcTarget, readRtc, err)
	}

	// 5. Timezone change
	if err := driver.ApplyTimezone("UTC"); err != nil {
		t.Fatalf("ApplyTimezone failed: %v", err)
	}
	tz, _ = driver.GetTimezone()
	if tz != "UTC" {
		t.Fatalf("expected UTC, got %s", tz)
	}
}

func TestStubClockDriverNoPermission(t *testing.T) {
	driver := NewStubClockDriver()
	driver.SetPermission(false)

	if err := driver.SetRealtime(time.Now()); err == nil {
		t.Error("expected error when SetRealtime without permission")
	}
	if err := driver.AdjTime(time.Second); err == nil {
		t.Error("expected error when AdjTime without permission")
	}
	if err := driver.WriteRTC(time.Now()); err == nil {
		t.Error("expected error when WriteRTC without permission")
	}
}

func TestStubClockDriverMissingRTC(t *testing.T) {
	driver := NewStubClockDriver()
	driver.SetRTCStatus(RtcStatusMissing)

	if _, err := driver.ReadRTC(); err == nil {
		t.Error("expected error reading missing RTC")
	}
	if err := driver.WriteRTC(time.Now()); err == nil {
		t.Error("expected error writing missing RTC")
	}
}
