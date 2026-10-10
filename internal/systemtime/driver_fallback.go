//go:build !linux

package systemtime

func newPlatformClockDriver() ClockDriver {
	return NewStubClockDriver()
}
