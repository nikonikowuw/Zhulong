package systemtime

import "time"

// ClockDriver abstracts operating system time and RTC hardware interactions.
type ClockDriver interface {
	// GetSystemTime returns current kernel system time.
	GetSystemTime() (time.Time, error)
	// SetRealtime forcefully sets CLOCK_REALTIME (Step adjustment).
	SetRealtime(t time.Time) error
	// AdjTime applies linear phase/frequency slew to CLOCK_REALTIME without stepping backward.
	AdjTime(offset time.Duration) error
	// ReadRTC reads UTC time from hardware RTC device (/dev/rtc*).
	ReadRTC() (time.Time, error)
	// WriteRTC writes UTC time into hardware RTC device.
	WriteRTC(t time.Time) error
	// GetRTCStatus checks if hardware RTC device is present and functional.
	GetRTCStatus() RtcStatus
	// GetTimezone returns the system's active IANA timezone name.
	GetTimezone() (string, error)
	// ApplyTimezone atomically applies the system timezone and reloads runtime timezone caches.
	ApplyTimezone(iana string) error
	// HasClockPermission checks whether the process has CAP_SYS_TIME or root privilege.
	HasClockPermission() bool
}

// NewDefaultClockDriver returns the platform-specific ClockDriver implementation.
func NewDefaultClockDriver() ClockDriver {
	return newPlatformClockDriver()
}
