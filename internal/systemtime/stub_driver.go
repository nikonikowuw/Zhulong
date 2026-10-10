package systemtime

import (
	"errors"
	"sync"
	"time"
)

// StubClockDriver is an in-memory mock implementation of ClockDriver for testing and non-Linux platforms.
type StubClockDriver struct {
	mu            sync.RWMutex
	offset        time.Duration
	rtcTime       time.Time
	rtcStatus     RtcStatus
	timezone      string
	hasPermission bool

	stepCalls     []time.Time
	slewCalls     []time.Duration
	rtcWriteCalls []time.Time
}

// NewStubClockDriver creates a new stub clock driver with default values.
func NewStubClockDriver() *StubClockDriver {
	return &StubClockDriver{
		rtcTime:       time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC),
		rtcStatus:     RtcStatusNormal,
		timezone:      "Asia/Shanghai",
		hasPermission: true,
	}
}

func (s *StubClockDriver) GetSystemTime() (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return time.Now().Add(s.offset), nil
}

func (s *StubClockDriver) SetRealtime(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasPermission {
		return errors.New("operation not permitted")
	}
	s.offset = time.Until(t)
	s.stepCalls = append(s.stepCalls, t)
	return nil
}

func (s *StubClockDriver) AdjTime(offset time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasPermission {
		return errors.New("operation not permitted")
	}
	s.offset += offset
	s.slewCalls = append(s.slewCalls, offset)
	return nil
}

func (s *StubClockDriver) ReadRTC() (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.rtcStatus != RtcStatusNormal {
		return time.Time{}, errors.New("hardware rtc device not available")
	}
	return s.rtcTime, nil
}

func (s *StubClockDriver) WriteRTC(t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasPermission {
		return errors.New("operation not permitted")
	}
	if s.rtcStatus != RtcStatusNormal {
		return errors.New("hardware rtc device not available")
	}
	utcTime := t.UTC()
	s.rtcTime = utcTime
	s.rtcWriteCalls = append(s.rtcWriteCalls, utcTime)
	return nil
}

func (s *StubClockDriver) GetRTCStatus() RtcStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rtcStatus
}

func (s *StubClockDriver) GetTimezone() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.timezone, nil
}

func (s *StubClockDriver) ApplyTimezone(iana string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	loc, err := time.LoadLocation(iana)
	if err != nil {
		return err
	}
	s.timezone = loc.String()
	return nil
}

func (s *StubClockDriver) HasClockPermission() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasPermission
}

// Test helper methods

func (s *StubClockDriver) SetPermission(p bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasPermission = p
}

func (s *StubClockDriver) SetRTCStatus(status RtcStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rtcStatus = status
}

func (s *StubClockDriver) SetRTCTime(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rtcTime = t.UTC()
}

func (s *StubClockDriver) GetStepCalls() []time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]time.Time, len(s.stepCalls))
	copy(res, s.stepCalls)
	return res
}

func (s *StubClockDriver) GetSlewCalls() []time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]time.Duration, len(s.slewCalls))
	copy(res, s.slewCalls)
	return res
}

func (s *StubClockDriver) GetRTCWrites() []time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	res := make([]time.Time, len(s.rtcWriteCalls))
	copy(res, s.rtcWriteCalls)
	return res
}
