// Package systemtime owns the host clock: reading it, disciplining it from NTP,
// applying a timezone, and mirroring corrections into the hardware RTC.
//
// Two invariants shape the code:
//
//   - Safety lives in TimeStateMachine, not in its callers. Sub-threshold offsets
//     are slewed, moderate ones stepped, and panic-level ones held until
//     consecutive probes agree that the same jump is real.
//   - Audit records describe events, not heartbeats. A steady-state slew never
//     reaches the audit trail; only clock steps, panic reviews, and the failure
//     and recovery edges do.
//
// ClockDriver is the only boundary that touches the operating system:
// linuxClockDriver implements it on Linux, and StubClockDriver serves the other
// platforms and the tests.
package systemtime
